package main

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/ujjwal313/boombrick/internal/audio/route"
	"github.com/ujjwal313/boombrick/internal/platform"
)

// outputs is the RG SP's choice of output, the quick menu's Output row: kept
// in a file in the data folder, so the background player, reading the same
// file, plays where the app was told to. nil off the RG SP. Set before the
// player is made.
var outputs *outputChoice

type outputChoice struct {
	path   string
	state  func() route.State // what is connected, without the choice
	chosen atomic.Uint32      // the choice, read from path once and kept here
}

func newOutputChoice(data string) *outputChoice {
	o := &outputChoice{path: filepath.Join(data, "output.txt"),
		state: func() route.State { return platform.AudioState("/") }}
	o.chosen.Store(uint32(o.load()))
	return o
}

// load is the choice saved, Auto when there is none.
func (o *outputChoice) load() route.Choice {
	b, err := os.ReadFile(o.path)
	if err != nil {
		return route.Auto
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || n < 0 || n >= len(route.Choices) {
		return route.Auto
	}
	return route.Choice(n)
}

// choice is the choice, from memory: the router asks twice a second from the
// thread that feeds the sound, which must never wait on the card.
func (o *outputChoice) choice() route.Choice { return route.Choice(o.chosen.Load()) }

// State is what is connected, with the choice: the router's state.
func (o *outputChoice) State() route.State {
	st := o.state()
	st.Choice = o.choice()
	return st
}

// Label is the Output row's value: where the music plays, after "Auto · "
// when that was not chosen.
func (o *outputChoice) Label() string {
	st := o.State()
	where := [...]string{"Speaker", "Headphones", "Bluetooth", "USB DAC"}[route.Resolve(st).Kind]
	if st.Choice == route.Auto || !st.Choice.Available(st) {
		return "Auto · " + where
	}
	return where
}

// Step moves the choice on by step among those available now, wrapping.
func (o *outputChoice) Step(step int) {
	st := o.State()
	var have []route.Choice
	for _, c := range route.Choices {
		if c.Available(st) {
			have = append(have, c)
		}
	}
	i := slices.Index(have, st.Choice)
	if i < 0 {
		i = 0
	}
	next := have[((i+step)%len(have)+len(have))%len(have)]
	o.chosen.Store(uint32(next))
	_ = os.WriteFile(o.path, []byte(strconv.Itoa(int(next))+"\n"), 0o644) // for the background player
}
