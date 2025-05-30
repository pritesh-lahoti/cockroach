// Copyright 2018 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package ordering

import (
	"github.com/cockroachdb/cockroach/pkg/sql/opt"
	"github.com/cockroachdb/cockroach/pkg/sql/opt/memo"
	"github.com/cockroachdb/cockroach/pkg/sql/opt/props"
)

func mutationCanProvideOrdering(expr memo.RelExpr, required *props.OrderingChoice) bool {
	// The mutation operator can always pass through ordering to its input.
	return true
}

func mutationBuildChildReqOrdering(
	parent memo.RelExpr, required *props.OrderingChoice, childIdx int,
) props.OrderingChoice {
	if childIdx != 0 {
		return props.OrderingChoice{}
	}

	// Remap each of the required columns to corresponding input columns.
	private := parent.Private().(*memo.MutationPrivate)

	optional := private.MapToInputCols(required.Optional)
	columns := make([]props.OrderingColumnChoice, len(required.Columns))
	for i := range required.Columns {
		colChoice := &required.Columns[i]
		columns[i] = props.OrderingColumnChoice{
			Group:      private.MapToInputCols(colChoice.Group),
			Descending: colChoice.Descending,
		}
	}
	return props.OrderingChoice{Optional: optional, Columns: columns}
}

func mutationBuildProvided(mem *memo.Memo, expr memo.RelExpr) opt.Ordering {
	private := expr.Private().(*memo.MutationPrivate)
	input := expr.Child(0).(memo.RelExpr)
	provided := input.ProvidedPhysical().Ordering

	// Construct FD set that includes mapping to/from input columns. This will
	// be used by remapProvided.
	var fdset props.FuncDepSet
	fdset.CopyFrom(&input.Relational().FuncDeps)
	private.AddEquivTableCols(mem.Metadata(), &fdset)

	// Ensure that provided ordering only uses projected columns.
	return remapProvided(provided, &fdset, expr.Relational().OutputCols)
}

func lockCanProvideOrdering(expr memo.RelExpr, required *props.OrderingChoice) bool {
	// The lock operator can always pass through ordering to its input.
	return true
}

func lockBuildChildReqOrdering(
	parent memo.RelExpr, required *props.OrderingChoice, childIdx int,
) props.OrderingChoice {
	if childIdx != 0 {
		return props.OrderingChoice{}
	}
	return *required
}

func lockBuildProvided(expr memo.RelExpr, required *props.OrderingChoice) opt.Ordering {
	lock := expr.(*memo.LockExpr)
	return lock.Input.ProvidedPhysical().Ordering
}
