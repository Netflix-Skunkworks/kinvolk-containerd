/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package tasks

import (
	"github.com/containerd/console"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/cmd/ctr/commands"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

var startCommand = cli.Command{
	Name:      "start",
	Usage:     "start a container that have been created",
	ArgsUsage: "CONTAINER",
	Flags: []cli.Flag{
		cli.BoolFlag{
			Name:  "null-io",
			Usage: "send all IO to /dev/null",
		},
		cli.StringFlag{
			Name:  "fifo-dir",
			Usage: "directory used for storing IO FIFOs",
		},
		cli.StringFlag{
			Name:  "pid-file",
			Usage: "file path to write the task's pid",
		},
	},
	Action: func(context *cli.Context) error {
		var (
			err error
			id  = context.Args().Get(0)
		)
		if id == "" {
			return errors.New("container id must be provided")
		}
		client, ctx, cancel, err := commands.NewClient(context)
		if err != nil {
			return err
		}
		defer cancel()
		container, err := client.LoadContainer(ctx, id)
		if err != nil {
			return err
		}

		spec, err := container.Spec(ctx)
		if err != nil {
			return err
		}

		var (
			tty    = spec.Process.Terminal
			opts   = getNewTaskOpts(context)
			ioOpts = []cio.Opt{cio.WithFIFODir(context.String("fifo-dir"))}
		)
		// First try to get the task if already created
		// TODO: how to pass a cio.NewCreator instead of a cio.NewAttach?
		//       Symptoms: exitting the shell with "exit" works but "ctrl-d" does not work
		task, err := container.Task(ctx, cio.NewAttach(cio.WithStdio))
		if err != nil {
			// Fallback on creating a new task
			task, err = NewTask(ctx, client, container, "", tty, context.Bool("null-io"), ioOpts, opts...)
			if err != nil {
				return err
			}
		}
		//defer task.Delete(ctx)
		if context.IsSet("pid-file") {
			if err := commands.WritePidFile(context.String("pid-file"), int(task.Pid())); err != nil {
				return err
			}
		}
		statusC, err := task.Wait(ctx)
		if err != nil {
			return err
		}

		var con console.Console
		if tty {
			con = console.Current()
			defer con.Reset()
			if err := con.SetRaw(); err != nil {
				return err
			}
		}
		if err := task.Start(ctx); err != nil {
			return err
		}
		if tty {
			if err := HandleConsoleResize(ctx, task, con); err != nil {
				logrus.WithError(err).Error("console resize")
			}
		} else {
			sigc := commands.ForwardAllSignals(ctx, task)
			defer commands.StopCatch(sigc)
		}

		status := <-statusC
		code, _, err := status.Result()
		if err != nil {
			return err
		}
		//if _, err := task.Delete(ctx); err != nil {
		//	return err
		//}
		if code != 0 {
			return cli.NewExitError("", int(code))
		}
		return nil
	},
}
