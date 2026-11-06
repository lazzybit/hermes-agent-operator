// Command hermes-gateway-init prepares one Hermes home before the gateway
// process starts. The operator declares mounts and arguments; this program
// creates the home layout, projects config and credentials, and applies an
// optional plugins.json. Later CR-driven setup belongs here.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

const usageText = `hermes-gateway-init prepares one Hermes home inside the gateway image.

It is the stable init entry at /usr/local/bin/hermes-gateway-init. The gateway
container uses the same image and starts Hermes with the command from the
Hermes resource. A custom image that does not implement this entry and these
flags loses home, config, credential, and plugin integration.

Flags:
  --profile       profile name; empty for the instance home
  --config        directory containing config.yaml and optional plugins.json
  --credentials   dotenv file linked as $HERMES_HOME/.env; empty skips it
  --plugins       plugins.json path; a missing file, [], or no entry for this profile installs nothing
  --plugins-dir   emptyDir that receives fetched plugin trees
  --plugin-deps   emptyDir that receives Python dependencies from pyproject.toml
  --messaging     messaging-channel.json path; a missing file or no entry for this profile installs nothing
  --generated     directory for generated profile material, usually /run/generated
`

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("hermes-gateway-init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	profile := fs.String("profile", "", "profile name; empty for the instance home")
	configDir := fs.String("config", "", "directory containing config.yaml")
	credentials := fs.String("credentials", "", "dotenv file to link as .env")
	plugins := fs.String("plugins", "", "optional plugins.json file")
	pluginsDir := fs.String("plugins-dir", "", "emptyDir directory for fetched plugin trees")
	depsDir := fs.String("plugin-deps", "", "emptyDir directory for plugin Python dependencies")
	messaging := fs.String("messaging", "", "optional messaging-channel.json file")
	generated := fs.String("generated", "/run/generated", "directory for generated profile material")
	fs.Usage = func() { fmt.Fprint(stderr, usageText) }
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 || *configDir == "" {
		fmt.Fprint(stderr, usageText)
		return 2
	}
	if err := initialize(options{
		profile:     *profile,
		configDir:   *configDir,
		credentials: *credentials,
		plugins:     *plugins,
		pluginsDir:  *pluginsDir,
		depsDir:     *depsDir,
		messaging:   *messaging,
		generated:   *generated,
	}); err != nil {
		fmt.Fprintf(stderr, "hermes-gateway-init: %v\n", err)
		return 1
	}
	return 0
}

type options struct {
	profile     string
	configDir   string
	credentials string
	plugins     string
	pluginsDir  string
	depsDir     string
	messaging   string
	generated   string
}

func initialize(opt options) error {
	home, err := initializeHome(opt.profile, opt.configDir, opt.credentials)
	if err != nil {
		return err
	}
	if err := applyMessaging(home, opt); err != nil {
		return err
	}
	if err := applyPlugins(opt.profile, home, opt.plugins, opt.pluginsDir, opt.depsDir); err != nil {
		return err
	}
	if opt.depsDir == "" {
		return nil
	}
	return writeSiteCustomizer(opt.depsDir)
}
