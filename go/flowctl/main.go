package main

import (
	"github.com/estuary/flow/go/runtime"
	"github.com/jessevdk/go-flags"
	"go.gazette.dev/core/cmd/gazctl/gazctlcmd"
	mbp "go.gazette.dev/core/mainboilerplate"
)

const iniFilename = "flow.ini"

func main() {
	var parser = flags.NewParser(nil, flags.HelpFlag|flags.PassDoubleDash)

	addCmd(parser, "test", "Locally test a Flow catalog", `
Locally test a Flow catalog.
		`, &cmdTest{})

	addCmd(parser, "check", "Check a Flow catalog for errors", `
Quickly load and validate a Flow catalog, and generate updated TypeScript types.
`, &cmdCheck{})

	addCmd(parser, "discover", "Discover available captures of an endpoint", `
Inspect a configured endpoint, and generate a Flow catalog of collections,
schemas, and capture bindings which reflect its available resources.

Discover is a two-stage workflow:

In the first invocation, the command will generate a stub
configuration YAML derived from the connector's specification.
The user reviews this YAML file, and updates it with appropriate
credentials and configuration.

In the second invocation, the command applies the completed
configuration to the endpoint and determines its available resource
bindings. It generates a Flow catalog YAML file with a Flow Capture
and associated Collection definitions. The user may then review,
update, refactor, and otherwise incorporate the generated entities
into their broader Flow catalog.
`, &cmdDiscover{})

	// The combine subcommand is hidden from help messages and such because we're uncertain of its
	// value, so don't want to expose it to users. We might just want to delete this, but leaving it
	// hidden for now. This was added to aid in debugging:
	// https://github.com/estuary/flow/issues/238
	addCmd(parser, "combine", "Combine documents from stdin", `
Read documents from stdin, validate and combine them on the collection's key, and print the results to stdout. The input documents must be JSON encoded and given one per line, and the output documents will be printed in the same way.
`, &cmdCombine{}).Hidden = true

	addCmd(parser, "json-schema", "Print the catalog JSON schema", `
Print the JSON schema specification of Flow catalogs, as understood by this
specific build of Flow. This JSON schema can be used to enable IDE support
and auto-completions.
`, &cmdJSONSchema{})

	addCmd(parser, "temp-data-plane", "Run an ephemeral, temporary local data plane", `
Run a local data plane by shelling out to start Etcd, Gazette, and the Flow consumer.
A local data plane is intended for local development and testing, and doesn't persist
fragments to the configured storage mappings of collections and Flow tasks.
Upon exit, all data is discarded.

If you intend to use a local data plane with 'flowctl api await', then you must
use the --poll flag, such that connectors poll their sources and then exit
rather than tailing sources continuously. This is uncommon, and typically only
used for integration testing workflows.
`, &cmdTempDataPlane{})

	addCmd(parser, "deploy", "Build a catalog and deploy it to a data plane", `
Build a catalog from --source. Then, activate it into a data plane.

If --block-and-cleanup, then await a Ctrl-C from the user and then fully remove
the deployment, cleaning up all its effects and restoring the data plane to
its original state.
`, &cmdDeploy{})

	serve, err := parser.Command.AddCommand("serve", "Serve a component of Flow", "", &struct{}{})
	mbp.Must(err, "failed to add command")

	addCmd(serve, "consumer", "Serve the Flow consumer", `
serve a Flow consumer with the provided configuration, until signaled to
exit (via SIGTERM). Upon receiving a signal, the consumer will seek to discharge
its responsible shards and will exit only when it can safely do so.
`, &runtime.FlowConsumerConfig{})

	// journals command - Add all journals sub-commands from gazctl under this command.
	journals, err := parser.Command.AddCommand("journals", "Interact with broker journals", "", gazctlcmd.JournalsCfg)
	mbp.Must(gazctlcmd.CommandRegistry.AddCommands("journals", journals, true), "failed to add commands")

	// shards command - Add all shards sub-commands from gazctl under this command.
	shards, err := parser.Command.AddCommand("shards", "Interact with consumer shards", "", gazctlcmd.ShardsCfg)
	mbp.Must(gazctlcmd.CommandRegistry.AddCommands("shards", shards, true), "failed to add commands")

	mbp.AddPrintConfigCmd(parser, iniFilename)

	apis, err := parser.Command.AddCommand("api", "Low-level APIs for automation", `
API commands which are designed for use in scripts and automated workflows,
including the Flow control plane. Users should not need to run API commands
directly (but are welcome to).
	`, &struct{}{})
	mbp.Must(err, "failed to add command")

	addCmd(apis, "build", "Build a Flow catalog", `
Build a Flow catalog.
`, &apiBuild{})

	addCmd(apis, "activate", "Activate a built Flow catalog", `
Activate tasks and collections of a Flow catalog.
`, &apiActivate{})

	addCmd(apis, "test", "Run tests of an activated catalog", `
Run tests of an activated catalog
`, &apiTest{})

	addCmd(apis, "await", "Wait for a catalog's dataflow to complete", `
Monitor a catalog's dataflow execution in the data-plane, and exit when it finishes.
`, &apiAwait{})

	addCmd(apis, "delete", "Delete from a built Flow catalog", `
Delete tasks and collections of a built Flow catalog.
`, &apiDelete{})

	// Parse config and start command
	mbp.MustParseConfig(parser, iniFilename)
}

func addCmd(to interface {
	AddCommand(string, string, string, interface{}) (*flags.Command, error)
}, a, b, c string, iface interface{}) *flags.Command {
	var cmd, err = to.AddCommand(a, b, c, iface)
	mbp.Must(err, "failed to add flags parser command")
	return cmd
}
