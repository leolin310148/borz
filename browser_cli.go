package main

import (
	"fmt"
	"strconv"

	"github.com/leolin310148/borz/internal/client"
	"github.com/leolin310148/borz/internal/config"
	borzprofile "github.com/leolin310148/borz/internal/profile"
)

// handleBrowser owns the managed browser's identity record: the file that says
// "this exact Chrome is mine". It exists so an identity mismatch is one command
// to inspect and one to resolve, instead of hand-editing borz's state.
func handleBrowser(cmdArgs []string, rawArgs []string, jsonOutput bool) {
	sub := "status"
	if len(cmdArgs) > 0 {
		sub = cmdArgs[0]
	}
	switch sub {
	case "status":
		handleBrowserStatus(rawArgs, jsonOutput)
	case "adopt":
		handleBrowserAdopt(rawArgs, jsonOutput)
	default:
		fatal(unknownSubcommandHint("browser", sub))
	}
}

// browserPortFromFlags resolves --port, defaulting to the recorded managed port.
func browserPortFromFlags(rawArgs []string) int {
	value, ok := getArgValueOK(rawArgs, "--port")
	if !ok {
		return client.ManagedBrowserPort()
	}
	port, err := strconv.Atoi(value)
	if err != nil || port <= 0 || port > 65535 {
		fatal("--port must be a TCP port between 1 and 65535")
	}
	return port
}

func handleBrowserStatus(rawArgs []string, jsonOutput bool) {
	target, err := borzprofile.ResolveTarget(config.Profile())
	if err != nil {
		fatal(err.Error())
	}
	if target.Kind != borzprofile.TransportManaged {
		if _, present := getArgValueOK(rawArgs, "--port"); present {
			fatal("--port applies only to managed profiles; this profile uses its configured endpoint")
		}
		if target.Kind == borzprofile.TransportRemote {
			fatal("browser status inspects local browser ownership; for remote profiles use daemon status")
		}
		alive := client.CheckCDPEndpoint(target.CDP.Host, target.CDP.Port, livenessProbeTimeout) == nil
		if jsonOutput {
			printJSON(map[string]interface{}{"transport": "cdp", "host": target.CDP.Host, "port": target.CDP.Port, "endpointAlive": alive, "owned": false})
		} else {
			fmt.Printf("External CDP: %s:%d\nEndpoint alive: %v\nOwnership: external (never adopted or closed by borz)\n", target.CDP.Host, target.CDP.Port, alive)
		}
		return
	}

	port := browserPortFromFlags(rawArgs)
	recordedID, liveID, recordedPort := client.ManagedBrowserIdentity(port)
	matches := recordedID != "" && recordedID == liveID && recordedPort == port

	if jsonOutput {
		printJSON(map[string]interface{}{
			"port":            port,
			"recordedPort":    recordedPort,
			"recordedBrowser": recordedID,
			"liveBrowser":     liveID,
			"identityMatches": matches,
			"stateFile":       config.ManagedStateFile(),
			"managedUserData": config.ManagedUserDataDir(),
		})
		return
	}

	fmt.Printf("Port:            %d\n", port)
	fmt.Printf("Recorded:        %s\n", describeBrowserID(recordedID, "none recorded yet"))
	fmt.Printf("Live on port:    %s\n", describeBrowserID(liveID, "nothing listening"))
	switch {
	case liveID == "":
		fmt.Println("State:           no browser to attach to; the next command launches one")
	case matches:
		fmt.Println("State:           OK — this is borz's own browser")
	default:
		fmt.Println("State:           MISMATCH — borz will not attach to or own this browser")
		fmt.Println("                 If it is borz's own (e.g. launched by an older borz),")
		fmt.Println("                 run 'borz browser adopt' to record it.")
	}
	fmt.Printf("State file:      %s\n", config.ManagedStateFile())
	fmt.Printf("Managed profile: %s\n", config.ManagedUserDataDir())
}

func describeBrowserID(id, empty string) string {
	if id == "" {
		return empty
	}
	return id
}

func handleBrowserAdopt(rawArgs []string, jsonOutput bool) {
	target, err := borzprofile.ResolveTarget(config.Profile())
	if err != nil {
		fatal(err.Error())
	}
	if target.Kind != borzprofile.TransportManaged {
		fatal("browser adopt applies only to managed profiles; external browsers remain externally owned")
	}

	port := browserPortFromFlags(rawArgs)
	adoptedPort, browserID, err := client.AdoptManagedBrowser(port)
	if err != nil {
		fatal(err.Error())
	}
	if jsonOutput {
		printJSON(map[string]interface{}{
			"port":      adoptedPort,
			"browser":   browserID,
			"adopted":   true,
			"stateFile": config.ManagedStateFile(),
		})
		return
	}
	fmt.Printf("Adopted the browser on 127.0.0.1:%d as borz's managed browser (%s).\n", adoptedPort, browserID)
	fmt.Println("Commands will attach to it from now on; borz owns it and closes it on daemon shutdown.")
}
