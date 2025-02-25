package labels

import (
	"testing"

	pf "github.com/estuary/protocols/flow"
	"github.com/stretchr/testify/require"
	pb "go.gazette.dev/core/broker/protocol"
)

func TestRangeSpecParsingCases(t *testing.T) {
	// Case: success.
	require.Equal(t, pf.RangeSpec{
		KeyBegin:    0xccddeeff,
		KeyEnd:      0xff00aabb,
		RClockBegin: 1 << 31,
		RClockEnd:   (1 << 32) - 1,
	}, MustParseRangeSpec(pb.MustLabelSet(
		KeyBegin, "ccddeeff",
		KeyEnd, "ff00aabb",
		RClockBegin, "80000000",
		RClockEnd, "ffffffff")))

	// Case: key is malformed hex.
	var _, err = ParseRangeSpec(pb.MustLabelSet(
		KeyBegin, "000000zz",
		KeyEnd, KeyEndMax,
		RClockBegin, RClockBeginMin,
		RClockEnd, RClockEndMax))
	require.Contains(t, err.Error(), "decoding hex-encoded label "+KeyBegin+": strconv.ParseUint: parsing \"000000zz\": invalid syntax")
	_, err = ParseRangeSpec(pb.MustLabelSet(
		KeyBegin, KeyBeginMin,
		KeyEnd, "000000zz",
		RClockBegin, RClockBeginMin,
		RClockEnd, RClockEndMax))
	require.Contains(t, err.Error(), "decoding hex-encoded label "+KeyEnd+": strconv.ParseUint: parsing \"000000zz\": invalid syntax")

	// Case: clock is malformed hex.
	_, err = ParseRangeSpec(pb.MustLabelSet(
		KeyBegin, KeyBeginMin,
		KeyEnd, KeyEndMax,
		RClockBegin, "000000zz",
		RClockEnd, RClockEndMax))
	require.Contains(t, err.Error(), "decoding hex-encoded label "+RClockBegin+": strconv.ParseUint: parsing \"000000zz\": invalid syntax")
	_, err = ParseRangeSpec(pb.MustLabelSet(
		KeyBegin, KeyBeginMin,
		KeyEnd, KeyEndMax,
		RClockBegin, RClockBeginMin,
		RClockEnd, "000000zz"))
	require.Contains(t, err.Error(), "decoding hex-encoded label "+RClockEnd+": strconv.ParseUint: parsing \"000000zz\": invalid syntax")

	// Case: clock is wrong length.
	_, err = ParseRangeSpec(pb.MustLabelSet(
		KeyBegin, KeyBeginMin,
		KeyEnd, KeyEndMax,
		RClockBegin, RClockBeginMin+"00",
		RClockEnd, RClockEndMax))
	require.Contains(t, err.Error(), "expected "+RClockBegin+" to be a 4-byte, hex encoded integer; got 0000000000")

	// Case: missing labels.
	_, err = ParseRangeSpec(pb.LabelSet{})
	require.EqualError(t, err, "expected one label for \"estuary.dev/key-begin\" (got [])")

	// Case: parses okay, but invalid range.
	_, err = ParseRangeSpec(pb.MustLabelSet(
		KeyBegin, KeyEndMax,
		KeyEnd, KeyBeginMin,
		RClockBegin, RClockBeginMin,
		RClockEnd, RClockEndMax))
	require.EqualError(t, err, "expected KeyBegin <= KeyEnd (ffffffff vs 00000000)")
	_, err = ParseRangeSpec(pb.MustLabelSet(
		KeyBegin, KeyBeginMin,
		KeyEnd, KeyEndMax,
		RClockBegin, RClockEndMax,
		RClockEnd, RClockBeginMin))
	require.EqualError(t, err, "expected RClockBegin <= RClockEnd (ffffffff vs 00000000)")
}

func TestRoundTripRangeSpecToLabels(t *testing.T) {
	var range_ = pf.RangeSpec{
		KeyBegin:    0xccddeeff,
		KeyEnd:      0xff00aabb,
		RClockBegin: 0xaabbaabb,
		RClockEnd:   0xddeeddee,
	}
	var labels = EncodeRange(range_, pb.MustLabelSet("other", "label"))

	require.Equal(t, pb.MustLabelSet(
		KeyBegin, "ccddeeff",
		KeyEnd, "ff00aabb",
		RClockBegin, "aabbaabb",
		RClockEnd, "ddeeddee",
		"other", "label",
	), labels)

	var recovered, err = ParseRangeSpec(labels)
	require.NoError(t, err)

	require.Equal(t, range_, recovered)
}
