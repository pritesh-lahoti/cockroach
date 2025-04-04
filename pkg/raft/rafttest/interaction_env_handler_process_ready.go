// This code has been modified from its original form by The Cockroach Authors.
// All modifications are Copyright 2024 The Cockroach Authors.
//
// Copyright 2019 The etcd Authors
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

package rafttest

import (
	"fmt"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/raft"
	"github.com/cockroachdb/cockroach/pkg/raft/raftpb"
	"github.com/cockroachdb/datadriven"
)

func (env *InteractionEnv) handleProcessReady(t *testing.T, d datadriven.TestData) error {
	idxs := nodeIdxs(t, d)
	for _, idx := range idxs {
		var err error
		if len(idxs) > 1 {
			fmt.Fprintf(env.Output, "> %d handling Ready\n", idx+1)
			env.withIndent(func() { err = env.ProcessReady(idx) })
		} else {
			err = env.ProcessReady(idx)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// ProcessReady runs Ready handling on the node with the given index.
func (env *InteractionEnv) ProcessReady(idx int) error {
	// TODO(tbg): Allow simulating crashes here.
	n := &env.Nodes[idx]
	rd := n.Ready()

	if !n.asyncWrites {
		send, _ := raft.SplitMessages(raftpb.PeerID(idx+1), rd.Messages)
		// When imitating a synchronous writes API, print only the outgoing
		// messages. The self-addressed responses conditional to storage syncs are
		// skipped, for compatibility with existing tests.
		// TODO(pav-kv): print all messages.
		fork := rd
		fork.Messages, _ = raft.SplitMessages(raftpb.PeerID(idx+1), rd.Messages)
		env.Output.WriteString(raft.DescribeReady(fork, defaultEntryFormatter))

		// TODO(pav-kv): use the same code paths as the asynchronous writes.
		if err := processAppend(n, rd.HardState, rd.Entries, rd.Snapshot); err != nil {
			return err
		} else if err := processApply(n, rd.CommittedEntries); err != nil {
			return err
		}

		env.Messages = append(env.Messages, send...)
		n.AdvanceHack(rd)
		return nil
	}

	env.Output.WriteString(raft.DescribeReady(rd, defaultEntryFormatter))

	for _, m := range rd.Messages {
		if raft.IsLocalMsgTarget(m.To) {
			switch m.Type {
			case raftpb.MsgStorageAppend:
				n.AppendWork = append(n.AppendWork, m)
			case raftpb.MsgStorageApply:
				n.ApplyWork = append(n.ApplyWork, m)
			default:
				panic(fmt.Sprintf("unexpected message type %s", m.Type))
			}
		} else {
			env.Messages = append(env.Messages, m)
		}
	}

	return nil
}
