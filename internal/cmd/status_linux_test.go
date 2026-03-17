//go:build linux

package cmd

import (
	"testing"
)

func TestStatusCmd_ServiceNotFound(t *testing.T) {
	origQuery := querySystemctl
	querySystemctl = func(args ...string) (string, error) {
		for _, arg := range args {
			if arg == "--property=LoadState" {
				return "not-found", nil
			}
			if arg == "--property=ActiveState" {
				return "inactive", nil
			}
			if arg == "--property=SubState" {
				return "dead", nil
			}
		}
		return "", nil
	}
	defer func() { querySystemctl = origQuery }()
	loadState, _ := querySystemctl("show", "--property=LoadState", "--value", "testvault")
	if loadState != "not-found" {
		t.Errorf("expected not-found, got %s", loadState)
	}
}

func TestStatusCmd_ServiceActive(t *testing.T) {
	origQuery := querySystemctl
	querySystemctl = func(args ...string) (string, error) {
		for _, arg := range args {
			if arg == "--property=LoadState" {
				return "loaded", nil
			}
			if arg == "--property=ActiveState" {
				return "active", nil
			}
			if arg == "--property=SubState" {
				return "running", nil
			}
		}
		return "", nil
	}
	defer func() { querySystemctl = origQuery }()
	activeState, _ := querySystemctl("show", "--property=ActiveState", "--value", "testvault")
	if activeState != "active" {
		t.Errorf("expected active, got %s", activeState)
	}
}
