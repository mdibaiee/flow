package ops

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/estuary/flow/go/flow/ops/testutil"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestLogLevelUnmarshaling(t *testing.T) {
	var testCases = []struct {
		input     string
		expect    log.Level
		expectErr error
	}{
		{input: `"inFormation"`, expect: log.InfoLevel},
		{input: `"info"`, expect: log.InfoLevel},
		{input: `"INFO"`, expect: log.InfoLevel},
		{input: `"WARN"`, expect: log.WarnLevel},
		{input: `"warning"`, expect: log.WarnLevel},
		{input: `"Trace"`, expect: log.TraceLevel},
		// This is just documenting the weird edge case.
		{input: `"Trace a line in the sand"`, expect: log.TraceLevel},
		{input: `"FATAL"`, expect: log.ErrorLevel},
		{input: `"panic"`, expect: log.ErrorLevel},
		{input: `{ "level": "info" }`, expectErr: INVALID_LOG_LEVEL},
		{input: `"not a real level"`, expectErr: INVALID_LOG_LEVEL},
		{input: `4`, expectErr: INVALID_LOG_LEVEL},
	}

	for _, testCase := range testCases {
		var actual jsonLogLevel
		var err = json.Unmarshal([]byte(testCase.input), &actual)
		if testCase.expectErr == nil {
			require.NoErrorf(t, err, "case failed: %+v", testCase)
		} else {
			require.Equalf(t, testCase.expectErr, err, "expectErr: %+v, actual: %v", testCase, err)
		}
		require.Equalf(t, testCase.expect, log.Level(actual), "mismatched: %+v, actual: %v", testCase, actual)
	}
}

func TestLogEventUnmarshaling(t *testing.T) {
	var doTest = func(line string, expected testutil.TestLogEvent) {
		var actual logEvent
		require.NoError(t, json.Unmarshal([]byte(line), &actual), "failed to parse line:", line)

		var actualEvent = testutil.TestLogEvent{
			Level:     log.Level(actual.Level),
			Timestamp: actual.Timestamp,
			Message:   actual.Message,
			Fields:    testutil.NormalizeFields(actual.Fields),
		}
		require.Truef(t, expected.Matches(&actualEvent), "mismatched event for line: %s, expected: %+v, actual: %+v", line, expected, actualEvent)
	}

	doTest(
		`{"lvl": "info", "msg": "foo", "ts": "2021-09-10T12:01:07.01234567Z"}`,
		testutil.TestLogEvent{
			Level:     log.InfoLevel,
			Message:   "foo",
			Timestamp: timestamp("2021-09-10T12:01:07.01234567Z"),
		},
	)
	doTest(
		`{"level": "TRace", "message": "yea boi", "fieldA": "valA", "ts": "2021-09-10T12:01:06.01234567Z"}`,
		testutil.TestLogEvent{
			Level:     log.TraceLevel,
			Message:   "yea boi",
			Timestamp: timestamp("2021-09-10T12:01:06.01234567Z"),
			Fields: map[string]interface{}{
				"fieldA": "valA",
			},
		},
	)
	doTest(
		`{"LVL": "not a real level", "message": {"wat": "huh"}, "fieldA": "valA", "ts": "not a real timestamp"}`,
		testutil.TestLogEvent{
			Fields: map[string]interface{}{
				"fieldA":  "valA",
				"LVL":     "not a real level",
				"ts":      "not a real timestamp",
				"message": map[string]interface{}{"wat": "huh"},
			},
		},
	)
	doTest(`{}`, testutil.TestLogEvent{})
}

func TestLogForwarding(t *testing.T) {
	var rawLogs = `
{"level": "TRace a line in the sand", "message": "yea boi", "fieldA": "valA", "ts": "2021-09-10T12:01:06.01234567Z"}
{"lVl": "iNfO", "MSG": "infoMessage", "fieldA": "valA", "ts": "2021-09-10T12:01:07.01234567Z"}
{"lEVEl": "warning", "Message": "warnMessage", "fieldA": "warnValA", "TimeStamp": "2021-09-10T12:01:08.01234567Z"}
2021-09-10T12:01:09.456Z INFO some text
{"foo": "bar"}
 a b c
 {"Lvl": "not even close to a real level"}
    `

	var publisher = testutil.NewTestLogPublisher(log.DebugLevel)

	var sourceDesc = "testSource"
	var fallbackLevel = log.WarnLevel
	ForwardLogs(sourceDesc, fallbackLevel, io.NopCloser(strings.NewReader(rawLogs)), publisher)

	var expected = []testutil.TestLogEvent{
		{
			Level:   log.TraceLevel,
			Message: "yea boi",
			Fields: map[string]interface{}{
				"fieldA":    "valA",
				"logSource": sourceDesc,
			},
			Timestamp: timestamp("2021-09-10T12:01:06.01234567Z"),
		},
		{
			Level:   log.InfoLevel,
			Message: "infoMessage",
			Fields: map[string]interface{}{
				"fieldA":    "valA",
				"logSource": sourceDesc,
			},
			Timestamp: timestamp("2021-09-10T12:01:07.01234567Z"),
		},
		{
			Level:   log.WarnLevel,
			Message: "warnMessage",
			Fields: map[string]interface{}{
				"fieldA":    "warnValA",
				"logSource": sourceDesc,
			},
			Timestamp: timestamp("2021-09-10T12:01:08.01234567Z"),
		},
		{
			Level:   log.WarnLevel,
			Message: "2021-09-10T12:01:09.456Z INFO some text",
			Fields: map[string]interface{}{
				"logSource": sourceDesc,
			},
		},
		{
			Level:   log.WarnLevel,
			Message: "",
			Fields: map[string]interface{}{
				"logSource": sourceDesc,
				"foo":       "bar",
			},
		},
		{
			Level:   log.WarnLevel,
			Message: " a b c",
			Fields: map[string]interface{}{
				"logSource": sourceDesc,
			},
		},
		{
			Level:   log.WarnLevel,
			Message: "",
			Fields: map[string]interface{}{
				"logSource": sourceDesc,
				"Lvl":       "not even close to a real level",
			},
		},
		{
			Level:   log.TraceLevel,
			Message: "finished forwarding logs",
			Fields: map[string]interface{}{
				"logSource": sourceDesc,
				"jsonLines": 5,
				"textLines": 2,
			},
		},
	}

	publisher.RequireEventsMatching(t, expected)
}

func timestamp(strVal string) time.Time {
	var t, err = time.Parse(time.RFC3339, strVal)
	if err != nil {
		panic(err)
	}
	return t
}
