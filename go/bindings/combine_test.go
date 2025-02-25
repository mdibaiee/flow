package bindings

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/estuary/flow/go/flow/ops"
	"github.com/estuary/flow/go/flow/ops/testutil"
	"github.com/estuary/protocols/catalog"
	"github.com/estuary/protocols/fdb/tuple"
	pf "github.com/estuary/protocols/flow"
	_ "github.com/mattn/go-sqlite3" // Import for registration side-effect.
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

// Validation failures are expected to be quite common, so we should pay special attention to how
// they're shown to the user.
func TestValidationFailuresAreLogged(t *testing.T) {
	var args = BuildArgs{
		Context:  context.Background(),
		FileRoot: "./testdata",
		BuildAPI_Config: pf.BuildAPI_Config{
			BuildId:    "fixture",
			Directory:  t.TempDir(),
			Source:     "file:///int-strings.flow.yaml",
			SourceType: pf.ContentType_CATALOG_SPEC,
		}}
	require.NoError(t, BuildCatalog(args))

	var collection *pf.CollectionSpec
	var schemaIndex *SchemaIndex

	require.NoError(t, catalog.Extract(args.OutputPath(), func(db *sql.DB) (err error) {
		if collection, err = catalog.LoadCollection(db, "int-strings"); err != nil {
			return fmt.Errorf("loading collection: %w", err)
		} else if bundle, err := catalog.LoadSchemaBundle(db); err != nil {
			return fmt.Errorf("loading bundle: %w", err)
		} else if schemaIndex, err = NewSchemaIndex(&bundle); err != nil {
			return fmt.Errorf("building index: %w", err)
		}
		return nil
	}))

	var logPublisher = testutil.NewTestLogPublisher(log.WarnLevel)
	combiner, err := NewCombine(logPublisher)
	require.NoError(t, err)
	defer combiner.Destroy()

	err = combiner.Configure(
		collection.Collection.String(),
		schemaIndex,
		collection.Collection,
		collection.SchemaUri,
		collection.UuidPtr,
		collection.KeyPtrs,
		nil,
	)
	require.NoError(t, err)

	require.NoError(t, combiner.CombineRight(json.RawMessage(`{"i": "not an int"}`)))

	err = combiner.Drain(func(_ bool, raw json.RawMessage, packedKey, packedFields []byte) error {
		require.Fail(t, "expected combine callback not to be called")
		return fmt.Errorf("not a real error")
	})
	require.Error(t, err)

	logPublisher.WaitForLogs(t, time.Millisecond*5000, 1)
	logPublisher.RequireEventsMatching(t, []testutil.TestLogEvent{
		{
			Level: log.ErrorLevel,
			Message: `document is invalid: {
  "basic_output": {
    "errors": [
      {
        "absoluteKeywordLocation": "file:///int-strings.flow.yaml?ptr=/collections/int-strings/schema#/properties/i",
        "error": "Invalid(Type(\"integer\"))",
        "instanceLocation": "/i",
        "keywordLocation": "#/properties/i"
      }
    ],
    "valid": false
  },
  "document": {
    "i": "not an int"
  }
}`,
			Fields: map[string]interface{}{
				"error":     `{"CombineError":{"PreReduceValidation":{"document":{"i":"not an int"},"basic_output":{"errors":[{"absoluteKeywordLocation":"file:///int-strings.flow.yaml?ptr=/collections/int-strings/schema#/properties/i","error":"Invalid(Type(\"integer\"))","instanceLocation":"/i","keywordLocation":"#/properties/i"}],"valid":false}}}}`,
				"logSource": "combine",
			},
		},
	})
}

func TestCombineBindings(t *testing.T) {
	var args = BuildArgs{
		Context:  context.Background(),
		FileRoot: "./testdata",
		BuildAPI_Config: pf.BuildAPI_Config{
			BuildId:    "fixture",
			Directory:  t.TempDir(),
			Source:     "file:///int-strings.flow.yaml",
			SourceType: pf.ContentType_CATALOG_SPEC,
		}}
	require.NoError(t, BuildCatalog(args))

	var collection *pf.CollectionSpec
	var schemaIndex *SchemaIndex

	require.NoError(t, catalog.Extract(args.OutputPath(), func(db *sql.DB) (err error) {
		if collection, err = catalog.LoadCollection(db, "int-strings"); err != nil {
			return fmt.Errorf("loading collection: %w", err)
		} else if bundle, err := catalog.LoadSchemaBundle(db); err != nil {
			return fmt.Errorf("loading bundle: %w", err)
		} else if schemaIndex, err = NewSchemaIndex(&bundle); err != nil {
			return fmt.Errorf("building index: %w", err)
		}
		return nil
	}))

	combiner, err := NewCombine(ops.StdLogger())
	require.NoError(t, err)

	// Loop to exercise re-use of a Combiner.
	for i := 0; i != 5; i++ {

		// Re-configure the Combiner every other iteration.
		if i%2 == 0 {
			err := combiner.Configure(
				collection.Collection.String(),
				schemaIndex,
				collection.Collection,
				collection.SchemaUri,
				collection.UuidPtr,
				collection.KeyPtrs,
				[]string{"/s/1", "/i"},
			)
			require.NoError(t, err)
		}

		require.NoError(t, combiner.CombineRight(json.RawMessage(`{"i": 32, "s": ["one"]}`)))
		require.NoError(t, combiner.CombineRight(json.RawMessage(`{"i": 42, "s": ["three"]}`)))
		require.NoError(t, pollExpectNoOutput(combiner.svc))
		require.NoError(t, combiner.ReduceLeft(json.RawMessage(`{"i": 42, "s": ["two"]}`)))
		require.NoError(t, combiner.CombineRight(json.RawMessage(`{"i": 32, "s": ["four"]}`)))

		if i%2 == 1 {
			// PrepareToDrain may optionally be called ahead of Drain.
			require.NoError(t, combiner.PrepareToDrain())
		}

		expectCombineFixture(t, combiner.Drain)
	}
}

func expectCombineFixture(t *testing.T, finish func(CombineCallback) error) {
	var expect = []struct {
		i int64
		s []string
	}{
		{32, []string{"one", "four"}},
		{42, []string{"two", "three"}},
	}

	require.NoError(t, finish(func(_ bool, raw json.RawMessage, packedKey, packedFields []byte) error {
		t.Log("doc", string(raw))

		var doc struct {
			I    int64
			S    []string
			Meta struct {
				UUID string
			} `json:"_meta"`
		}

		require.NoError(t, json.Unmarshal(raw, &doc))
		require.Equal(t, expect[0].i, doc.I)
		require.Equal(t, expect[0].s, doc.S)
		require.Equal(t, string(pf.DocumentUUIDPlaceholder), doc.Meta.UUID)

		require.Equal(t, tuple.Tuple{doc.I}.Pack(), packedKey)
		require.Equal(t, tuple.Tuple{doc.S[1], doc.I}.Pack(), packedFields)

		expect = expect[1:]
		return nil
	}))
}
