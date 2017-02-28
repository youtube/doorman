// Copyright 2015 The etcd Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package command

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/coreos/etcd/clientv3"
	"github.com/spf13/cobra"
	"golang.org/x/net/context"
)

var (
	watchRev         int64
	watchPrefix      bool
	watchInteractive bool
)

// NewWatchCommand returns the cobra command for "watch".
func NewWatchCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watch [options] [key or prefix] [range_end]",
		Short: "Watches events stream on keys or prefixes",
		Run:   watchCommandFunc,
	}

	cmd.Flags().BoolVarP(&watchInteractive, "interactive", "i", false, "Interactive mode")
	cmd.Flags().BoolVar(&watchPrefix, "prefix", false, "Watch on a prefix if prefix is set")
	cmd.Flags().Int64Var(&watchRev, "rev", 0, "Revision to start watching")

	return cmd
}

// watchCommandFunc executes the "watch" command.
func watchCommandFunc(cmd *cobra.Command, args []string) {
	if watchInteractive {
		watchInteractiveFunc(cmd, args)
		return
	}
	if len(args) < 1 || len(args) > 2 {
		ExitWithError(ExitBadArgs, fmt.Errorf("watch in non-interactive mode requires one or two arguments as key or prefix, with range end"))
	}

	opts := []clientv3.OpOption{clientv3.WithRev(watchRev)}
	key := args[0]
	if len(args) == 2 {
		if watchPrefix {
			ExitWithError(ExitBadArgs, fmt.Errorf("`range_end` and `--prefix` cannot be set at the same time, choose one"))
		}
		opts = append(opts, clientv3.WithRange(args[1]))
	}

	if watchPrefix {
		opts = append(opts, clientv3.WithPrefix())
	}
	c := mustClientFromCmd(cmd)
	wc := c.Watch(context.TODO(), key, opts...)
	printWatchCh(wc)
	err := c.Close()
	if err == nil {
		ExitWithError(ExitInterrupted, fmt.Errorf("watch is canceled by the server"))
	}
	ExitWithError(ExitBadConnection, err)
}

func watchInteractiveFunc(cmd *cobra.Command, args []string) {
	c := mustClientFromCmd(cmd)

	reader := bufio.NewReader(os.Stdin)

	for {
		l, err := reader.ReadString('\n')
		if err != nil {
			ExitWithError(ExitInvalidInput, fmt.Errorf("Error reading watch request line: %v", err))
		}
		l = strings.TrimSuffix(l, "\n")

		args := argify(l)
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Invalid command %s (command type or key is not provided)\n", l)
			continue
		}

		if args[0] != "watch" {
			fmt.Fprintf(os.Stderr, "Invalid command %s (only support watch)\n", l)
			continue
		}

		flagset := NewWatchCommand().Flags()
		err = flagset.Parse(args[1:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid command %s (%v)\n", l, err)
			continue
		}
		moreargs := flagset.Args()
		if len(moreargs) < 1 || len(moreargs) > 2 {
			fmt.Fprintf(os.Stderr, "Invalid command %s (Too few or many arguments)\n", l)
			continue
		}
		var key string
		_, err = fmt.Sscanf(moreargs[0], "%q", &key)
		if err != nil {
			key = moreargs[0]
		}
		opts := []clientv3.OpOption{clientv3.WithRev(watchRev)}
		if len(moreargs) == 2 {
			if watchPrefix {
				fmt.Fprintf(os.Stderr, "`range_end` and `--prefix` cannot be set at the same time, choose one\n")
				continue
			}
			opts = append(opts, clientv3.WithRange(moreargs[1]))
		}
		if watchPrefix {
			opts = append(opts, clientv3.WithPrefix())
		}
		ch := c.Watch(context.TODO(), key, opts...)
		go printWatchCh(ch)
	}
}

func printWatchCh(ch clientv3.WatchChan) {
	for resp := range ch {
		display.Watch(resp)
	}
}
