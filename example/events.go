// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package main

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/cilium/hive"
	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/job"
	"github.com/cilium/hive/script"
	"github.com/cilium/stream"
)

// eventsCell provides the ExampleEvents API for subscribing
// to a stream of example events.
var eventsCell = cell.Module(
	"example-events",
	"Provides a stream of example events",

	cell.Provide(
		newExampleEvents,
		watchEventsCommand,
	),
)

type ExampleEvent struct {
	Message string
}

type ExampleEvents interface {
	stream.Observable[ExampleEvent]
}

type exampleEventSource struct {
	stream.Observable[ExampleEvent]

	emit     func(ExampleEvent) // Emits an item to 'src'
	complete func(error)        // Completes 'src'
}

func (es *exampleEventSource) emitter(ctx context.Context, health cell.Health) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	defer es.complete(nil)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			es.emit(makeEvent())
		}
	}
}

// makeEvent generates a random event
func makeEvent() ExampleEvent {
	var prefixes = []string{
		"Thrusters set to",
		"Main engine damage at",
		"Laser power set to",
		"Remaining hypercannon fuel:",
		"Reserve of peanut butter sandwiches:",
		"Crew morale at",
		"Elevator music volume now set to",
		"Mission completion: ",
	}

	prefixIdx := rand.Intn(len(prefixes))
	percentage := rand.Intn(100)

	return ExampleEvent{
		Message: fmt.Sprintf("%s %d%%", prefixes[prefixIdx], percentage),
	}
}

func newExampleEvents(lc cell.Lifecycle, jobs job.Registry, health cell.Health) ExampleEvents {
	es := &exampleEventSource{}
	// Multicast() constructs a one-to-many observable to which items can be emitted.
	es.Observable, es.emit, es.complete = stream.Multicast[ExampleEvent]()

	// Create a new job group and add emitter as a one-shot job.
	g := jobs.NewGroup(health, lc)
	g.Add(job.OneShot("emitter", es.emitter))
	return es
}

// watchEventsCommand defines the hive script command "events" that subscribes
// to events.
func watchEventsCommand(ee ExampleEvents) hive.ScriptCmdOut {
	return hive.NewScriptCmd(
		"events",
		script.Command(
			script.CmdUsage{Summary: "Watch events"},
			func(s *script.State, args ...string) (script.WaitFunc, error) {
				for e := range stream.ToChannel(s.Context(), ee) {
					s.Logf("%s\n", e)
					s.FlushLog()
				}
				return nil, nil
			},
		),
	)
}
