// Copyright 2013 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"github.com/globocom/tsuru/app"
	"github.com/globocom/tsuru/app/bind"
	"github.com/globocom/tsuru/db"
	"github.com/globocom/tsuru/log"
	"github.com/globocom/tsuru/provision"
	"github.com/globocom/tsuru/queue"
	"labix.org/v2/mgo/bson"
	. "launchpad.net/gocheck"
	stdlog "log"
	"strings"
)

func (s *S) TestHandleMessage(c *C) {
	s.provisioner.PrepareOutput([]byte("exported"))
	a := app.App{
		Name: "nemesis",
		Units: []app.Unit{
			{
				Name:    "i-00800",
				State:   "started",
				Machine: 19,
			},
		},
		Env: map[string]bind.EnvVar{
			"http_proxy": {
				Name:   "http_proxy",
				Value:  "http://myproxy.com:3128/",
				Public: true,
			},
		},
		State: string(provision.StatusStarted),
	}
	err := db.Session.Apps().Insert(a)
	c.Assert(err, IsNil)
	defer db.Session.Apps().Remove(bson.M{"name": a.Name})
	msg := queue.Message{Action: app.RegenerateApprc, Args: []string{a.Name}}
	handle(&msg)
	cmds := s.provisioner.GetCmds("", &a)
	c.Assert(cmds, HasLen, 1)
	output := strings.Replace(cmds[0].Cmd, "\n", " ", -1)
	outputRegexp := `^cat > /home/application/apprc <<END # generated by tsuru.*`
	outputRegexp += `export http_proxy="http://myproxy.com:3128/" END $`
	c.Assert(output, Matches, outputRegexp)
}

func (s *S) TestHandleMessageWithSpecificUnit(c *C) {
	s.provisioner.PrepareOutput([]byte("exported"))
	a := app.App{
		Name: "nemesis",
		Units: []app.Unit{
			{
				Name:    "nemesis/0",
				State:   "started",
				Machine: 19,
			},
			{
				Name:    "nemesis/1",
				State:   "started",
				Machine: 20,
			},
			{
				Name:    "nemesis/2",
				State:   "started",
				Machine: 23,
			},
		},
		Env: map[string]bind.EnvVar{
			"http_proxy": {
				Name:   "http_proxy",
				Value:  "http://myproxy.com:3128/",
				Public: true,
			},
		},
		State: string(provision.StatusStarted),
	}
	err := db.Session.Apps().Insert(a)
	c.Assert(err, IsNil)
	defer db.Session.Apps().Remove(bson.M{"name": a.Name})
	msg := queue.Message{Action: app.RegenerateApprc, Args: []string{a.Name, "nemesis/1"}}
	handle(&msg)
	cmds := s.provisioner.GetCmds("", &a)
	c.Assert(cmds, HasLen, 1)
	output := strings.Replace(cmds[0].Cmd, "\n", " ", -1)
	outputRegexp := `^cat > /home/application/apprc <<END # generated by tsuru.*`
	outputRegexp += `export http_proxy="http://myproxy.com:3128/" END $`
	c.Assert(output, Matches, outputRegexp)
}

func (s *S) TestHandleMessageErrors(c *C) {
	var data = []struct {
		action      string
		args        []string
		unitName    string
		expectedLog string
	}{
		{
			action:      "unknown-action",
			args:        []string{"does not matter"},
			expectedLog: `Error handling "unknown-action": invalid action.`,
		},
		{
			action: app.StartApp,
			args:   []string{"nemesis"},
			expectedLog: `Error handling "start-app" for the app "nemesis":` +
				` The status of the app and all units should be "started" (the app is "pending").`,
		},
		{
			action: app.StartApp,
			args:   []string{"totem", "totem/0", "totem/1"},
			expectedLog: `Error handling "start-app" for the app "totem":` +
				` The status of the app and all units should be "started" (the app is "started").`,
		},
		{
			action: app.RegenerateApprc,
			args:   []string{"nemesis"},
			expectedLog: `Error handling "regenerate-apprc" for the app "nemesis":` +
				` The status of the app and all units should be "started" (the app is "pending").`,
		},
		{
			action: app.RegenerateApprc,
			args:   []string{"totem", "totem/0", "totem/1"},
			expectedLog: `Error handling "regenerate-apprc" for the app "totem":` +
				` The status of the app and all units should be "started" (the app is "started").`,
		},
		{
			action:      app.RegenerateApprc,
			args:        []string{"unknown-app"},
			expectedLog: `Error handling "regenerate-apprc": app "unknown-app" does not exist.`,
		},
		{
			action:      app.RegenerateApprc,
			expectedLog: `Error handling "regenerate-apprc": this action requires at least 1 argument.`,
		},
		{
			action: app.RegenerateApprc,
			args:   []string{"marathon"},
			expectedLog: `Error handling "regenerate-apprc" for the app "marathon":` +
				` the app is in "error" state.`,
		},
		{
			action: app.RegenerateApprc,
			args:   []string{"territories"},
			expectedLog: `Error handling "regenerate-apprc" for the app "territories":` +
				` the app is down.`,
		},
	}
	var buf bytes.Buffer
	a := app.App{Name: "nemesis", State: "pending"}
	err := db.Session.Apps().Insert(a)
	c.Assert(err, IsNil)
	defer db.Session.Apps().Remove(bson.M{"name": a.Name})
	a = app.App{
		Name:  "totem",
		State: "started",
		Units: []app.Unit{
			{Name: "totem/0", State: "pending"},
			{Name: "totem/1", State: "started"},
		},
	}
	err = db.Session.Apps().Insert(a)
	c.Assert(err, IsNil)
	defer db.Session.Apps().Remove(bson.M{"name": a.Name})
	a = app.App{Name: "marathon", State: "error"}
	err = db.Session.Apps().Insert(a)
	c.Assert(err, IsNil)
	defer db.Session.Apps().Remove(bson.M{"name": a.Name})
	a = app.App{Name: "territories", State: "down"}
	err = db.Session.Apps().Insert(a)
	c.Assert(err, IsNil)
	defer db.Session.Apps().Remove(bson.M{"name": a.Name})
	log.SetLogger(stdlog.New(&buf, "", 0))
	for _, d := range data {
		message := queue.Message{Action: d.action}
		if len(d.args) > 0 {
			message.Args = d.args
		}
		handle(&message)
		defer queue.Delete(&message) // Sanity
	}
	content := buf.String()
	lines := strings.Split(content, "\n")
	for i, d := range data {
		var found bool
		for j := i; j < len(lines); j++ {
			if lines[j] == d.expectedLog {
				found = true
				break
			}
		}
		if !found {
			c.Errorf("\nWant: %q.\nGot:\n%s", d.expectedLog, content)
		}
	}
}

func (s *S) TestHandleRestartAppMessage(c *C) {
	s.provisioner.PrepareOutput([]byte("started"))
	a := app.App{
		Name: "nemesis",
		Units: []app.Unit{
			{
				Name:    "i-00800",
				State:   "started",
				Machine: 19,
			},
		},
		State: string(provision.StatusStarted),
	}
	err := db.Session.Apps().Insert(a)
	c.Assert(err, IsNil)
	defer db.Session.Apps().Remove(bson.M{"name": a.Name})
	message := queue.Message{Action: app.StartApp, Args: []string{a.Name}}
	handle(&message)
	cmds := s.provisioner.GetCmds("/var/lib/tsuru/hooks/restart", &a)
	c.Assert(cmds, HasLen, 1)
}

func (s *S) TestUnitListStarted(c *C) {
	var tests = []struct {
		input    []app.Unit
		expected bool
	}{
		{
			[]app.Unit{
				{State: "started"},
				{State: "started"},
				{State: "started"},
			},
			true,
		},
		{nil, true},
		{
			[]app.Unit{
				{State: "started"},
				{State: "blabla"},
			},
			false,
		},
	}
	for _, t := range tests {
		l := UnitList(t.input)
		if got := l.Started(); got != t.expected {
			c.Errorf("l.Started(): want %v. Got %v.", t.expected, got)
		}
	}
}
