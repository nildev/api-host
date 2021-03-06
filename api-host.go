package main // import "github.com/nildev/api-host"

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/nildev/api-host/config"
	"github.com/nildev/api-host/server"
	"github.com/nildev/api-host/version"
	"github.com/rakyll/globalconf"

	// Import these as after code is generated it will be required
	log "github.com/Sirupsen/logrus"
	_ "github.com/nildev/lib/codegen"
	_ "github.com/nildev/lib/utils"
)

const (
	DefaultConfigFile = "/etc/api-host/api-host.conf"
)

var (
	GitHash        = ""
	BuiltTimestamp = ""
	Version        = ""
	ctxLog         *log.Entry
)

func init() {
	version.Version = Version
	version.GitHash = GitHash
	version.BuiltTimestamp = BuiltTimestamp

	log.SetFormatter(&log.JSONFormatter{})
	log.SetOutput(os.Stdout)
	log.SetLevel(log.WarnLevel)
}

func main() {
	ctxLog = log.WithField("version", version.Version).WithField("git-hash", version.GitHash).WithField("build-time", version.BuiltTimestamp)
	userset := flag.NewFlagSet("apihostd", flag.ExitOnError)

	printVersion := userset.Bool("version", false, "Print the version and exit")
	cfgPath := userset.String("config", DefaultConfigFile, fmt.Sprintf("Path to config file. apihostd will look for a config at %s by default.", DefaultConfigFile))

	err := userset.Parse(os.Args[1:])
	if err == flag.ErrHelp {
		userset.Usage()
		os.Exit(1)
	}

	args := userset.Args()
	if len(args) == 1 && args[0] == "version" {
		*printVersion = true
	} else if len(args) != 0 {
		userset.Usage()
		os.Exit(1)
	}

	if *printVersion {
		fmt.Printf("Version: %s \n", version.Version)
		fmt.Printf("Git hash: %s \n", version.GitHash)
		fmt.Printf("Build timestamp: %s \n", version.BuiltTimestamp)
		os.Exit(0)
	}

	cfgset := flag.NewFlagSet("apihostd", flag.ExitOnError)
	// Generic
	cfgset.Int("verbosity", 0, "Logging level")
	cfgset.String("ip", "", "Server IP to bind")
	cfgset.String("port", "", "Port to listen on")
	// CORS
	cfgset.String("cors_allowed_origins", "*", "A list of origins a cross-domain request can be executed from")
	cfgset.String("cors_allowed_methods", "GET,POST,DELETE", "A list of methods the client is allowed to use with cross-domain requests")
	cfgset.String("cors_allowed_headers", "origin, content-type, accept, authorization", "A list of non simple headers the client is allowed to use with cross-domain requests.")
	cfgset.String("cors_exposed_headers", "", "Indicates which headers are safe to expose to the API of a CORS API specification")
	cfgset.Bool("cors_allow_credentials", false, "Indicates whether the request can include user credentials like cookies, HTTP authentication or client side SSL certificates.")
	cfgset.Int("cors_max_age", 0, "Indicates how long (in seconds) the results of a preflight request can be cached.")
	cfgset.Bool("cors_options_pass_through", false, "Instructs preflight to let other potential next handlers to process the OPTIONS method.")
	cfgset.Bool("cors_debug", false, "Debugging flag adds additional output to debug server side CORS issues.")
	// JWT
	cfgset.String("jwt_sign_key", "", "JWT signing key")

	globalconf.Register("", cfgset)
	cfg, err := getConfig(cfgset, *cfgPath)
	if err != nil {
		ctxLog.Fatalf(err.Error())
	}

	srv, err := server.New(*cfg)
	if err != nil {
		ctxLog.Fatalf("Failed creating Server: %v", err.Error())
	}
	srv.Run()

	reconfigure := func() {
		ctxLog.Infof("Reloading configuration from %s", *cfgPath)

		cfg, err := getConfig(cfgset, *cfgPath)
		if err != nil {
			ctxLog.Fatalf(err.Error())
		}

		ctxLog.Infof("Restarting server components")
		srv.Stop()

		srv, err = server.New(*cfg)
		if err != nil {
			ctxLog.Fatalf(err.Error())
		}
		srv.Run()
	}

	shutdown := func() {
		ctxLog.Infof("Gracefully shutting down")
		srv.Stop()
		srv.Purge()
		os.Exit(0)
	}

	writeState := func() {
		ctxLog.Infof("Dumping server state")

		encoded, err := json.Marshal(srv)
		if err != nil {
			ctxLog.Errorf("Failed to dump server state: %v", err)
			return
		}

		if _, err := os.Stdout.Write(encoded); err != nil {
			ctxLog.Errorf("Failed to dump server state: %v", err)
			return
		}

		os.Stdout.Write([]byte("\n"))
	}

	signals := map[os.Signal]func(){
		syscall.SIGHUP:  reconfigure,
		syscall.SIGTERM: shutdown,
		syscall.SIGINT:  shutdown,
		syscall.SIGUSR1: writeState,
		syscall.SIGABRT: shutdown,
	}

	listenForSignals(signals)
}

func getConfig(flagset *flag.FlagSet, userCfgFile string) (*config.Config, error) {
	opts := globalconf.Options{EnvPrefix: "API_HOSTD_"}

	if userCfgFile != "" {
		// Fail hard if a user-provided config is not usable
		fi, err := os.Stat(userCfgFile)
		if err != nil {
			ctxLog.Fatalf("Unable to use config file %s: %v", userCfgFile, err)
		}
		if fi.IsDir() {
			ctxLog.Fatalf("Provided config %s is a directory, not a file", userCfgFile)
		}
		opts.Filename = userCfgFile
	} else if _, err := os.Stat(DefaultConfigFile); err == nil {
		opts.Filename = DefaultConfigFile
	}

	gconf, err := globalconf.NewWithOptions(&opts)
	if err != nil {
		return nil, err
	}

	gconf.ParseSet("", flagset)

	cfg := config.Config{
		Verbosity: (*flagset.Lookup("verbosity")).Value.(flag.Getter).Get().(int),
		IP:        (*flagset.Lookup("ip")).Value.(flag.Getter).Get().(string),
		Port:      (*flagset.Lookup("port")).Value.(flag.Getter).Get().(string),
		Secret:    (*flagset.Lookup("jwt_sign_key")).Value.(flag.Getter).Get().(string),

		CORSAllowedOrigins:     config.StringToSlice((*flagset.Lookup("cors_allowed_origins")).Value.(flag.Getter).Get().(string)),
		CORSAllowedMethods:     config.StringToSlice((*flagset.Lookup("cors_allowed_methods")).Value.(flag.Getter).Get().(string)),
		CORSAllowedHeaders:     config.StringToSlice((*flagset.Lookup("cors_allowed_headers")).Value.(flag.Getter).Get().(string)),
		CORSExposedHeaders:     config.StringToSlice((*flagset.Lookup("cors_exposed_headers")).Value.(flag.Getter).Get().(string)),
		CORSAllowCredentials:   (*flagset.Lookup("cors_allow_credentials")).Value.(flag.Getter).Get().(bool),
		CORSMaxAge:             (*flagset.Lookup("cors_max_age")).Value.(flag.Getter).Get().(int),
		CORSOptionsPassThrough: (*flagset.Lookup("cors_options_pass_through")).Value.(flag.Getter).Get().(bool),
		CORSDebug:              (*flagset.Lookup("cors_debug")).Value.(flag.Getter).Get().(bool),
	}

	log.SetLevel(log.Level(cfg.Verbosity))

	ctxLog.Infof("Loaded config: [%+v]", cfg)

	return &cfg, nil
}

func listenForSignals(sigmap map[os.Signal]func()) {
	sigchan := make(chan os.Signal, 1)

	for k := range sigmap {
		signal.Notify(sigchan, k)
	}

	for true {
		sig := <-sigchan
		handler, ok := sigmap[sig]
		if ok {
			handler()
		}
	}
}
