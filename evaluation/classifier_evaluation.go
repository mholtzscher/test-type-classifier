// Command evaluation reports classifier quality from reviewed JSON Lines records.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/mholtzscher/test-type-classifier/internal/classification"
)

type evaluationRecord struct {
	ID                   string             `json:"id"`
	Repository           string             `json:"repository"`
	Split                string             `json:"split"`
	Language             string             `json:"language"`
	HumanPrimary         string             `json:"human_primary"`
	HumanTraits          []string           `json:"human_traits"`
	PredictedPrimary     string             `json:"predicted_primary"`
	PredictedTraits      []string           `json:"predicted_traits"`
	PrimaryProbabilities map[string]float64 `json:"primary_probabilities"`
	Confidence           float64            `json:"confidence"`
	PendingHumanReview   bool               `json:"pending_human_review"`
	Model                string             `json:"model"`
	QuestionSetVersion   string             `json:"question_set_version"`
	ExtractorVersion     string             `json:"extractor_version"`
	ReviewerAPrimary     string             `json:"reviewer_a_primary"`
	ReviewerBPrimary     string             `json:"reviewer_b_primary"`
	AdjudicationNote     string             `json:"adjudication_note"`
}

type countMetrics struct {
	TruePositive  int
	FalsePositive int
	FalseNegative int
}

func (m countMetrics) precision() float64 {
	if m.TruePositive+m.FalsePositive == 0 {
		return 0
	}
	return float64(m.TruePositive) / float64(m.TruePositive+m.FalsePositive)
}

func (m countMetrics) recall() float64 {
	if m.TruePositive+m.FalseNegative == 0 {
		return 0
	}
	return float64(m.TruePositive) / float64(m.TruePositive+m.FalseNegative)
}

func (m countMetrics) f1() float64 {
	p, r := m.precision(), m.recall()
	if p+r == 0 {
		return 0
	}
	return 2 * p * r / (p + r)
}

type evaluationSummary struct {
	Records         int
	Primary         map[string]countMetrics
	Traits          map[string]countMetrics
	Confusion       map[string]map[string]int
	Accepted        int
	AcceptedCorrect int
	ByLanguage      map[string]countMetrics
	ByRepository    map[string]countMetrics
	BySplit         map[string]countMetrics
	ReviewerKappa   float64
}

func main() {
	labelsPath := flag.String("labels", "evaluation/labels.jsonl", "reviewed JSON Lines dataset")
	minimumConfidence := flag.Float64("min-confidence", 0, "candidate acceptance confidence")
	minimumMargin := flag.Float64("min-margin", 0, "candidate acceptance probability margin")
	flag.Parse()
	if (*minimumConfidence > 0) != (*minimumMargin > 0) {
		fmt.Fprintln(os.Stderr, "evaluation: min-confidence and min-margin must both be set")
		os.Exit(2)
	}

	file, err := os.Open(*labelsPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "evaluation: open labels:", err)
		os.Exit(1)
	}
	defer file.Close()

	records, err := readEvaluationRecords(file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "evaluation: read labels:", err)
		os.Exit(1)
	}
	summary, err := summarizeEvaluation(records, *minimumConfidence, *minimumMargin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "evaluation:", err)
		os.Exit(1)
	}
	writeEvaluationSummary(os.Stdout, summary)
}

func readEvaluationRecords(reader io.Reader) ([]evaluationRecord, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	var records []evaluationRecord
	for line := 1; scanner.Scan(); line++ {
		var record evaluationRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		if record.PendingHumanReview {
			continue
		}
		if record.HumanPrimary == "" || record.PredictedPrimary == "" {
			return nil, fmt.Errorf("line %d: reviewed row requires human_primary and predicted_primary", line)
		}
		if err := validateEvaluationRecord(record); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func validateEvaluationRecord(record evaluationRecord) error {
	for field, value := range map[string]string{
		"repository": record.Repository, "split": record.Split, "language": record.Language,
		"model": record.Model, "question_set_version": record.QuestionSetVersion,
		"extractor_version": record.ExtractorVersion, "reviewer_a_primary": record.ReviewerAPrimary,
		"reviewer_b_primary": record.ReviewerBPrimary,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("reviewed row requires %s", field)
		}
	}
	if record.Split != "development" && record.Split != "holdout" {
		return fmt.Errorf("invalid split %q", record.Split)
	}
	if record.Language != "go" && record.Language != "kotlin" {
		return fmt.Errorf("invalid language %q", record.Language)
	}
	for field, value := range map[string]string{"human_primary": record.HumanPrimary, "predicted_primary": record.PredictedPrimary, "reviewer_a_primary": record.ReviewerAPrimary, "reviewer_b_primary": record.ReviewerBPrimary} {
		if _, ok := classification.ParsePrimaryTestType(value); !ok {
			return fmt.Errorf("invalid %s %q", field, value)
		}
	}
	for _, trait := range append(append([]string{}, record.HumanTraits...), record.PredictedTraits...) {
		if _, ok := classification.ParseTestTrait(trait); !ok {
			return fmt.Errorf("invalid trait %q", trait)
		}
	}
	return nil
}

func summarizeEvaluation(records []evaluationRecord, minimumConfidence, minimumMargin float64) (evaluationSummary, error) {
	if len(records) == 0 {
		return evaluationSummary{}, errors.New("no adjudicated predictions; pending seed rows are not evaluation evidence")
	}
	summary := evaluationSummary{
		Records: len(records), Primary: map[string]countMetrics{}, Traits: map[string]countMetrics{},
		Confusion: map[string]map[string]int{}, ByLanguage: map[string]countMetrics{}, ByRepository: map[string]countMetrics{}, BySplit: map[string]countMetrics{},
	}
	repositorySplits := map[string]string{}
	var reviewerA, reviewerB []string
	for _, record := range records {
		if prior, ok := repositorySplits[record.Repository]; ok && prior != record.Split {
			return evaluationSummary{}, fmt.Errorf("repository %q appears in both %s and %s splits", record.Repository, prior, record.Split)
		}
		repositorySplits[record.Repository] = record.Split
		reviewerA = append(reviewerA, record.ReviewerAPrimary)
		reviewerB = append(reviewerB, record.ReviewerBPrimary)
		labels := map[string]bool{record.HumanPrimary: true, record.PredictedPrimary: true}
		for label := range labels {
			metric := summary.Primary[label]
			switch {
			case record.HumanPrimary == label && record.PredictedPrimary == label:
				metric.TruePositive++
			case record.PredictedPrimary == label:
				metric.FalsePositive++
			case record.HumanPrimary == label:
				metric.FalseNegative++
			}
			summary.Primary[label] = metric
		}
		if summary.Confusion[record.HumanPrimary] == nil {
			summary.Confusion[record.HumanPrimary] = map[string]int{}
		}
		summary.Confusion[record.HumanPrimary][record.PredictedPrimary]++

		updateBinaryGroup(summary.ByLanguage, record.Language, record.HumanPrimary == record.PredictedPrimary)
		updateBinaryGroup(summary.ByRepository, record.Repository, record.HumanPrimary == record.PredictedPrimary)
		updateBinaryGroup(summary.BySplit, record.Split, record.HumanPrimary == record.PredictedPrimary)
		updateTraitMetrics(summary.Traits, record.HumanTraits, record.PredictedTraits)

		if minimumConfidence > 0 && minimumMargin > 0 && record.Confidence >= minimumConfidence && probabilityMargin(record.PrimaryProbabilities) >= minimumMargin {
			summary.Accepted++
			if record.HumanPrimary == record.PredictedPrimary {
				summary.AcceptedCorrect++
			}
		}
	}
	summary.ReviewerKappa = cohensKappa(reviewerA, reviewerB)
	return summary, nil
}

func cohensKappa(a, b []string) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	agreement := 0
	countsA, countsB := map[string]int{}, map[string]int{}
	for i := range a {
		if a[i] == b[i] {
			agreement++
		}
		countsA[a[i]]++
		countsB[b[i]]++
	}
	n := float64(len(a))
	observed := float64(agreement) / n
	expected := 0.0
	for label, countA := range countsA {
		expected += (float64(countA) / n) * (float64(countsB[label]) / n)
	}
	if expected == 1 {
		return 1
	}
	return (observed - expected) / (1 - expected)
}

func updateTraitMetrics(metrics map[string]countMetrics, human, predicted []string) {
	humanSet, predictedSet := makeStringSet(human), makeStringSet(predicted)
	all := makeStringSet(append(append([]string{}, human...), predicted...))
	for trait := range all {
		metric := metrics[trait]
		switch {
		case humanSet[trait] && predictedSet[trait]:
			metric.TruePositive++
		case predictedSet[trait]:
			metric.FalsePositive++
		case humanSet[trait]:
			metric.FalseNegative++
		}
		metrics[trait] = metric
	}
}

func makeStringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

func updateBinaryGroup(groups map[string]countMetrics, name string, correct bool) {
	metric := groups[name]
	if correct {
		metric.TruePositive++
	} else {
		metric.FalsePositive++
	}
	groups[name] = metric
}

func probabilityMargin(probabilities map[string]float64) float64 {
	values := make([]float64, 0, len(probabilities))
	for _, probability := range probabilities {
		values = append(values, probability)
	}
	sort.Sort(sort.Reverse(sort.Float64Slice(values)))
	if len(values) < 2 {
		return 0
	}
	return values[0] - values[1]
}

func writeEvaluationSummary(writer io.Writer, summary evaluationSummary) {
	macroF1 := 0.0
	for _, metric := range summary.Primary {
		macroF1 += metric.f1()
	}
	macroF1 /= float64(len(summary.Primary))
	fmt.Fprintf(writer, "records: %d\nreviewer_cohens_kappa: %.3f\nmacro_f1: %.3f\n", summary.Records, summary.ReviewerKappa, macroF1)
	writeMetricMap(writer, "primary", summary.Primary)
	writeMetricMap(writer, "traits", summary.Traits)
	fmt.Fprintln(writer, "confusion_matrix:")
	for _, actual := range sortedMapKeys(summary.Confusion) {
		for _, predicted := range sortedMapKeys(summary.Confusion[actual]) {
			fmt.Fprintf(writer, "  %s -> %s: %d\n", actual, predicted, summary.Confusion[actual][predicted])
		}
	}
	coverage, accuracy := 0.0, 0.0
	if summary.Records > 0 {
		coverage = float64(summary.Accepted) / float64(summary.Records)
	}
	if summary.Accepted > 0 {
		accuracy = float64(summary.AcceptedCorrect) / float64(summary.Accepted)
	}
	fmt.Fprintf(writer, "accepted_coverage: %.3f\naccepted_accuracy: %.3f\n", coverage, accuracy)
	writeAccuracyGroups(writer, "by_language", summary.ByLanguage)
	writeAccuracyGroups(writer, "by_repository", summary.ByRepository)
	writeAccuracyGroups(writer, "by_split", summary.BySplit)
}

func writeMetricMap(writer io.Writer, heading string, metrics map[string]countMetrics) {
	fmt.Fprintln(writer, heading+":")
	for _, label := range sortedMapKeys(metrics) {
		metric := metrics[label]
		fmt.Fprintf(writer, "  %s precision=%.3f recall=%.3f f1=%.3f\n", label, metric.precision(), metric.recall(), metric.f1())
	}
}

func writeAccuracyGroups(writer io.Writer, heading string, groups map[string]countMetrics) {
	fmt.Fprintln(writer, heading+":")
	for _, name := range sortedMapKeys(groups) {
		metric := groups[name]
		total := metric.TruePositive + metric.FalsePositive
		fmt.Fprintf(writer, "  %s accuracy=%.3f n=%d\n", name, float64(metric.TruePositive)/float64(total), total)
	}
}

func sortedMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
