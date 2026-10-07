package utility

import "testing"

func TestGenerateRedisKeyForServiceSfdcFieldMapping_UsesSequenceID(t *testing.T) {
	gen := NewRedisKeyGenerator()

	key := gen.GenerateRedisKeyForServiceSfdcFieldMapping(12)

	if key != "decision-managerserviceSfdcFieldMapping:12" {
		t.Fatalf("unexpected key: %s", key)
	}
}

func TestGenerateRedisKeyForAllServiceSfdcFieldMapping_UsesSingleGlobalKey(t *testing.T) {
	gen := NewRedisKeyGenerator()

	key := gen.GenerateRedisKeyForAllServiceSfdcFieldMapping()

	if key != "decision-managerserviceSfdcFieldMapping:all" {
		t.Fatalf("unexpected global key: %s", key)
	}

	seqKey := gen.GenerateRedisKeyForServiceSfdcFieldMapping(99)
	if key == seqKey {
		t.Fatalf("global key should differ from sequence-specific key: %s", key)
	}
}

func TestGenerateRedisKeyForPartnerServiceMapping_SkipsEmptySegments(t *testing.T) {
	gen := NewRedisKeyGenerator()

	key := gen.GenerateRedisKeyForPartnerServiceMapping(
		"AirtelFinance",
		"PQ",
		"Personal Loan",
		"", // sourcingProgram
		"", // loanCategory
		"", // customerType
		"", // productLine
		"", // salesChannelPartnerName
		"", // sourcingChannel
		"", // nameOfConsolidator
		"loan-decision",
	)

	expected := "decision-managerpartnerServiceMapping:AirtelFinance:PQ:Personal Loan:loan-decision"
	if key != expected {
		t.Fatalf("expected key %q, got %q", expected, key)
	}
}
