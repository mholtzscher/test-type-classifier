package classification

import "github.com/mholtzscher/test-type-classifier/internal/evidence"

// traitFeatureRules maps a deterministic trait to the parser feature names that
// establish it. A feature name is a stable identifier emitted by a parser, not
// free-form prose, so this mapping stays sound when question wording changes.
var traitFeatureRules = map[TestTrait][]string{
	TraitFuzz:          {evidence.FeatureGoFuzzFunction},
	TraitGolden:        {evidence.FeatureGoldenComparison},
	TraitSnapshot:      {evidence.FeatureSnapshotAssertion},
	TraitParameterized: {evidence.FeatureGoTableDrivenSubtest, evidence.FeatureKotlinParameterizedAnnotation},
	TraitConcurrency:   {evidence.FeatureGoParallelCall},
}

// ParserTraits derives trait predictions from deterministic parser features.
// The returned map contains only present traits and always uses the parser
// source, so callers can merge model judgments without losing provenance.
func ParserTraits(ev evidence.TestEvidence) map[TestTrait]TraitPrediction {
	features := make(map[string]bool, len(ev.Features))
	for _, feature := range ev.Features {
		features[feature.Name] = true
	}
	traits := make(map[TestTrait]TraitPrediction)
	for trait, names := range traitFeatureRules {
		for _, name := range names {
			if features[name] {
				traits[trait] = TraitPrediction{Present: true, Probability: 1, Source: TraitSourceParser}
				break
			}
		}
	}
	return traits
}
