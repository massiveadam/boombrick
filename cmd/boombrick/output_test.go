package main

import (
	"testing"

	"github.com/ujjwal313/boombrick/internal/audio/route"
)

func TestOutputChoice(t *testing.T) {
	st := route.State{Bluetooth: "bt_AA"}
	o := &outputChoice{path: t.TempDir() + "/output.txt", state: func() route.State { return st }}
	defer func() {
		// Saved for the background player, which reads it as it starts.
		o2 := &outputChoice{path: o.path}
		if o2.load() != o.choice() {
			t.Errorf("saved %v, chosen %v", o2.load(), o.choice())
		}
	}()
	if got := o.Label(); got != "Auto · Bluetooth" {
		t.Fatalf("auto: %q", got)
	}
	o.Step(1) // Auto -> this device
	if got := o.Label(); got != "Speaker" {
		t.Fatalf("this device: %q", got)
	}
	o.Step(1) // -> the headset (no DAC, so it is skipped next)
	if got := o.Label(); got != "Bluetooth" {
		t.Fatalf("headset: %q", got)
	}
	o.Step(1) // wraps to Auto
	if o.choice() != route.Auto {
		t.Fatalf("wrap: %v", o.choice())
	}
	o.Step(-1) // back to the headset
	if o.choice() != route.ChooseBT {
		t.Fatalf("back: %v", o.choice())
	}
	// The headset gone: the choice stays saved, the sound goes where Auto says.
	st = route.State{Wired: true}
	if got := o.Label(); got != "Auto · Headphones" {
		t.Fatalf("headset gone: %q", got)
	}
}
