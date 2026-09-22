// Package report renders classifications as deterministic terminal output. It
// never modifies source files and writes only to the writer it is given.
package report

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/mholtzscher/test-type-classifier/internal/classification"
)

// TableReporter writes one stable row per classification. Rows are sorted by
// test identifier and traits are sorted alphabetically so repeated runs over
// the same evidence produce byte-identical output.
type TableReporter struct {
	out io.Writer
}

// NewTableReporter returns a reporter that writes to out.
func NewTableReporter(out io.Writer) *TableReporter {
	return &TableReporter{out: out}
}

// WriteReport writes the primary type, traits, confidence, review status, and
// test identifier for every classification.
func (r *TableReporter) WriteReport(ctx context.Context, results []classification.Classification) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rows := make([]tableRow, 0, len(results))
	for _, result := range results {
		rows = append(rows, newTableRow(result))
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].test < rows[j].test })

	writer := tabwriter.NewWriter(r.out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "TYPE\tTRAITS\tCONF\tSTATUS\tTEST"); err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n",
			row.primary, row.traits, row.confidence, row.status, row.test); err != nil {
			return err
		}
	}
	return writer.Flush()
}

type tableRow struct {
	primary    string
	traits     string
	confidence string
	status     string
	test       string
}

func newTableRow(result classification.Classification) tableRow {
	return tableRow{
		primary:    string(result.Prediction.Primary),
		traits:     formatTraits(result.Prediction.Traits),
		confidence: fmt.Sprintf("%.2f", result.Prediction.Confidence),
		status:     string(result.Decision),
		test:       result.Evidence.ID.String(),
	}
}

// formatTraits lists the present traits in a stable alphabetical order.
func formatTraits(traits map[classification.TestTrait]classification.TraitPrediction) string {
	present := make([]string, 0, len(traits))
	for trait, prediction := range traits {
		if prediction.Present {
			present = append(present, string(trait))
		}
	}
	sort.Strings(present)
	return strings.Join(present, ",")
}

// SkippedFile records a discovered path that could not be read or parsed. A run
// reports these so no file fails silently.
type SkippedFile struct {
	Path   string
	Reason string
}

// WriteSkippedFiles prints the skipped files, sorted by path, under a heading.
// It writes nothing when there are no skipped files.
func WriteSkippedFiles(out io.Writer, skipped []SkippedFile) error {
	if len(skipped) == 0 {
		return nil
	}
	ordered := append([]SkippedFile(nil), skipped...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Path != ordered[j].Path {
			return ordered[i].Path < ordered[j].Path
		}
		return ordered[i].Reason < ordered[j].Reason
	})
	if _, err := fmt.Fprintln(out, "SKIPPED FILES"); err != nil {
		return err
	}
	for _, file := range ordered {
		if _, err := fmt.Fprintf(out, "  %s: %s\n", file.Path, file.Reason); err != nil {
			return err
		}
	}
	return nil
}
