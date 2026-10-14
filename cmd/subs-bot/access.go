package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// access is how the bot reaches the panel: the Panel API and the Bot API proxy, each with
// its token.
type access struct {
	panelURL, panelToken string
	botURL, botToken     string
}

// hostState is the part of the host's addons/state.json the bot reads when it runs as a
// process of the admin's own (source external): no container hands it the environment, so
// it takes its tokens where the host keeps them.
type hostState struct {
	Adapters map[string]struct {
		PanelToken string `json:"panel_token"`
		Status     string `json:"status"`
	} `json:"adapters"`
	PanelAPI string `json:"panel_api"`
}

// accessOfState reads the addon id's access from the host's state file.
func accessOfState(path, id string) (access, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return access{}, err
	}
	var st hostState
	if err := json.Unmarshal(b, &st); err != nil {
		return access{}, fmt.Errorf("%s: %w", path, err)
	}
	a, ok := st.Adapters[id]
	switch {
	case !ok:
		return access{}, fmt.Errorf("the addon %q is not installed (no entry in %s)", id, path)
	case a.Status != "running":
		return access{}, fmt.Errorf("the addon %q is %q, not running", id, a.Status)
	case a.PanelToken == "":
		return access{}, fmt.Errorf("the addon %q has no panel token: is it of protocol 2?", id)
	case st.PanelAPI == "":
		return access{}, errors.New("the host gives no address of the panel's API for addons")
	}
	base := "http://" + st.PanelAPI
	// The same token opens the Bot API proxy; the number before it means nothing.
	return access{panelURL: base + "/panel/v2", panelToken: a.PanelToken, botURL: base, botToken: "0:" + a.PanelToken}, nil
}

// accessOfEnv is the environment a container of the host gets.
func accessOfEnv() (access, error) {
	a := access{
		panelURL: os.Getenv("MIKAN_PANEL_URL"), panelToken: os.Getenv("MIKAN_PANEL_TOKEN"),
		botURL: os.Getenv("MIKAN_BOT_API_URL"), botToken: os.Getenv("MIKAN_BOT_TOKEN"),
	}
	for k, v := range map[string]string{"MIKAN_PANEL_URL": a.panelURL, "MIKAN_PANEL_TOKEN": a.panelToken,
		"MIKAN_BOT_API_URL": a.botURL, "MIKAN_BOT_TOKEN": a.botToken} {
		if v == "" {
			return access{}, fmt.Errorf("%s is not set", k)
		}
	}
	return a, nil
}

// watchState runs the bot with the access in the state file and starts it again whenever
// that access changes (the admin reinstalled the addon: new tokens), until ctx ends. While
// the file names no running addon it waits.
func watchState(ctx context.Context, path, id string, every time.Duration, log *slog.Logger, run func(context.Context, access)) {
	var (
		cur    access
		cancel context.CancelFunc = func() {}
		done   chan struct{}
		told   string
	)
	stop := func() {
		cancel()
		if done != nil {
			<-done
			done = nil
		}
	}
	defer stop()
	for {
		a, err := accessOfState(path, id)
		if err != nil {
			if err.Error() != told {
				told = err.Error()
				log.Warn("waiting for the addon's access", "err", err)
			}
			// Removed or stopped: its tokens no longer open anything.
			stop()
			cur = access{}
		} else {
			told = ""
		}
		if err == nil && a != cur {
			if done != nil {
				log.Info("the addon's access changed: starting again")
			}
			stop()
			cur = a
			rctx, c := context.WithCancel(ctx)
			cancel = c
			done = make(chan struct{})
			go func(d chan struct{}) { defer close(d); run(rctx, a) }(done)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}
