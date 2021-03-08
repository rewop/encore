package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"

	daemonpb "encr.dev/proto/encore/daemon"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var dbCmd = &cobra.Command{
	Use:   "db",
	Short: "Database management commands",
}

var resetAll bool

var dbResetCmd = &cobra.Command{
	Use:   "reset [servicenames...]",
	Short: "Resets the databases for the given services, or the current directory if unspecified",

	Run: func(command *cobra.Command, args []string) {
		appRoot, relPath := determineAppRoot()
		svcNames := args
		if resetAll && len(svcNames) > 0 {
			fatal("cannot specify both --all and service names")
		}
		if !resetAll && len(svcNames) == 0 {
			pkgs, err := resolvePackages(filepath.Join(appRoot, relPath), ".")
			if err != nil {
				log.Fatal().Err(err).Msg("could not resolve packages")
			}
			svcNames = []string{filepath.Base(pkgs[0])}
		}

		ctx := context.Background()
		daemon := setupDaemon(ctx)
		stream, err := daemon.DBReset(ctx, &daemonpb.DBResetRequest{
			AppRoot:  appRoot,
			Services: svcNames,
		})
		if err != nil {
			fatal("reset databases: ", err)
		}
		streamCommandOutput(stream)
	},
}

var dbEnv string

var dbShellCmd = &cobra.Command{
	Use:   "shell [service-name]",
	Short: "Connects to the database via psql shell",
	Args:  cobra.MaximumNArgs(1),

	Run: func(command *cobra.Command, args []string) {
		appRoot, relPath := determineAppRoot()
		ctx := context.Background()
		daemon := setupDaemon(ctx)
		svcName := ""
		if len(args) > 0 {
			svcName = args[0]
		} else {
			// Find the enclosing service by looking for the "migrations" folder
		SvcNameLoop:
			for p := relPath; p != "."; p = filepath.Dir(p) {
				absPath := filepath.Join(appRoot, p)
				if _, err := os.Stat(filepath.Join(absPath, "migrations")); err == nil {
					pkgs, err := resolvePackages(absPath, ".")
					if err == nil && len(pkgs) > 0 {
						svcName = filepath.Base(pkgs[0])
						break SvcNameLoop
					}
				}
			}
			if svcName == "" {
				fatal("could not find an Encore service with a database in this directory (or any of the parent directories).\n\n" +
					"Note: You can specify a service name to connect to it directly using the command 'encore db shell <service-name>'.")
			}
		}

		resp, err := daemon.DBConnect(ctx, &daemonpb.DBConnectRequest{
			AppRoot: appRoot,
			SvcName: svcName,
			EnvName: dbEnv,
		})
		if err != nil {
			fatalf("could not connect to the database for service %s: %v", svcName, err)
		}

		// If we have the psql binary, use that.
		// Otherwise fall back to docker.
		var cmd *exec.Cmd
		if p, err := exec.LookPath("psql"); err == nil {
			cmd = exec.Command(p, resp.Dsn)
		} else {
			fmt.Fprintln(os.Stderr, "encore: no 'psql' executable found in $PATH; using docker to run 'psql' instead.\n\nNote: install psql to hide this message.")
			dsn := resp.Dsn

			if runtime.GOOS == "darwin" {
				// Docker for Mac's networking setup requires
				// using "host.docker.internal" instead of "localhost"
				for _, rep := range []string{"localhost", "127.0.0.1"} {
					dsn = strings.Replace(dsn, rep, "host.docker.internal", -1)
				}
			}

			cmd = exec.Command("docker", "run", "-it", "--rm", "--network=host", "postgres", "psql", dsn)
		}
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin

		if err := cmd.Start(); err != nil {
			log.Fatal().Err(err).Msg("failed to start psql")
		}
		signal.Ignore(os.Interrupt)
		if err := cmd.Wait(); err != nil {
			log.Fatal().Err(err).Msg("psql failed")
		}
	},
}

var dbProxyPort int32

var dbProxyCmd = &cobra.Command{
	Use:   "proxy [--env=<name>]",
	Short: "Sets up a proxy tunnel to the database",

	Run: func(command *cobra.Command, args []string) {
		appRoot, _ := determineAppRoot()
		interrupt := make(chan os.Signal, 1)
		signal.Notify(interrupt, os.Interrupt)

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			<-interrupt
			cancel()
		}()

		daemon := setupDaemon(ctx)
		stream, err := daemon.DBProxy(ctx, &daemonpb.DBProxyRequest{
			AppRoot: appRoot,
			EnvName: dbEnv,
			Port:    dbProxyPort,
		})
		if err != nil {
			log.Fatal().Err(err).Msg("could not setup db proxy")
		}
		streamCommandOutput(stream)
	},
}

var dbConnURICmd = &cobra.Command{
	Use:   "conn-uri [servicename]",
	Short: "Outputs the database connection string",
	Args:  cobra.MaximumNArgs(1),

	Run: func(command *cobra.Command, args []string) {
		appRoot, relPath := determineAppRoot()
		ctx := context.Background()
		daemon := setupDaemon(ctx)
		svcName := ""
		if len(args) > 0 {
			svcName = args[0]
		} else {
			// Find the enclosing service by looking for the "migrations" folder
		SvcNameLoop:
			for p := relPath; p != "."; p = filepath.Dir(p) {
				absPath := filepath.Join(appRoot, p)
				if _, err := os.Stat(filepath.Join(absPath, "migrations")); err == nil {
					pkgs, err := resolvePackages(absPath, ".")
					if err == nil && len(pkgs) > 0 {
						svcName = filepath.Base(pkgs[0])
						break SvcNameLoop
					}
				}
			}
			if svcName == "" {
				fatal("could not find Encore service with a database in this directory (or any parent directory).\n\n" +
					"Note: You can specify a service name to connect to it directly using the command 'encore db conn-uri <service-name>'.")
			}
		}

		resp, err := daemon.DBConnect(ctx, &daemonpb.DBConnectRequest{
			AppRoot: appRoot,
			SvcName: svcName,
			EnvName: dbEnv,
		})
		if err != nil {
			fatalf("could not connect to the database for service %s: %v", svcName, err)
		}

		fmt.Fprintln(os.Stdout, resp.Dsn)
	},
}

func init() {
	rootCmd.AddCommand(dbCmd)

	dbResetCmd.Flags().BoolVar(&resetAll, "all", false, "Reset all services in the application")
	dbCmd.AddCommand(dbResetCmd)

	dbShellCmd.Flags().StringVarP(&dbEnv, "env", "e", "", "Environment name to connect to (such as \"production\")")
	dbCmd.AddCommand(dbShellCmd)

	dbProxyCmd.Flags().StringVarP(&dbEnv, "env", "e", "", "Environment name to connect to (such as \"production\")")
	dbProxyCmd.Flags().Int32VarP(&dbProxyPort, "port", "p", 0, "Port to listen on (defaults to a random port)")
	dbCmd.AddCommand(dbProxyCmd)

	dbConnURICmd.Flags().StringVarP(&dbEnv, "env", "e", "", "Environment name to connect to (such as \"production\")")
	dbCmd.AddCommand(dbConnURICmd)
}