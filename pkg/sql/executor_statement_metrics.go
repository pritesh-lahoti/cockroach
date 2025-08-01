// Copyright 2017 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package sql

import (
	"context"
	"time"

	"github.com/cockroachdb/cockroach/pkg/sql/appstatspb"
	"github.com/cockroachdb/cockroach/pkg/sql/contentionpb"
	"github.com/cockroachdb/cockroach/pkg/sql/idxrecommendations"
	"github.com/cockroachdb/cockroach/pkg/sql/sem/tree"
	"github.com/cockroachdb/cockroach/pkg/sql/sessionphase"
	"github.com/cockroachdb/cockroach/pkg/sql/sqlstats"
	"github.com/cockroachdb/cockroach/pkg/util/log"
	"github.com/cockroachdb/cockroach/pkg/util/metric"
	"github.com/cockroachdb/cockroach/pkg/util/metric/aggmetric"
)

// EngineMetrics groups a set of SQL metrics.
type EngineMetrics struct {
	// The subset of SELECTs that are requested to be processed through DistSQL.
	DistSQLSelectCount *metric.Counter
	// The subset of SELECTs that were executed by DistSQL with full or partial
	// distribution.
	DistSQLSelectDistributedCount *metric.Counter
	DistSQLExecLatency            metric.IHistogram
	DistSQLServiceLatency         metric.IHistogram

	SQLOptPlanCacheHits   *metric.Counter
	SQLOptPlanCacheMisses *metric.Counter

	StatementFingerprintCount *metric.UniqueCounter
	SQLExecLatencyDetail      *metric.HistogramVec

	SQLExecLatency metric.IHistogram
	// Exec Latency of only non-AOST queries
	SQLExecLatencyConsistent metric.IHistogram
	// Exec Latency of only AOST queries
	SQLExecLatencyHistorical metric.IHistogram

	SQLServiceLatency *aggmetric.SQLHistogram
	// Service Latency of only non-AOST queries
	SQLServiceLatencyConsistent metric.IHistogram
	// Service Latency of only AOST queries
	SQLServiceLatencyHistorical metric.IHistogram

	SQLTxnLatency       *aggmetric.SQLHistogram
	SQLTxnsOpen         *aggmetric.SQLGauge
	SQLActiveStatements *aggmetric.SQLGauge
	SQLContendedTxns    *metric.Counter

	// TxnAbortCount counts transactions that were aborted, either due
	// to non-retryable errors, or retryable errors when the client-side
	// retry protocol is not in use.
	TxnAbortCount *metric.Counter

	// FailureCount counts non-retryable errors in open transactions.
	FailureCount *aggmetric.SQLCounter

	// StatementTimeoutCount tracks the number of statement failures due
	// to exceeding the statement timeout.
	StatementTimeoutCount *metric.Counter

	// TransactionTimeoutCount tracks the number of statement failures due
	// to exceeding the transaction timeout.
	TransactionTimeoutCount *metric.Counter

	// FullTableOrIndexScanCount counts the number of full table or index scans.
	FullTableOrIndexScanCount *aggmetric.SQLCounter

	// FullTableOrIndexScanRejectedCount counts the number of queries that were
	// rejected because of the `disallow_full_table_scans` guardrail.
	FullTableOrIndexScanRejectedCount *metric.Counter

	// TxnRetryCount counts the number of automatic transaction retries that
	// have occurred.
	TxnRetryCount *metric.Counter

	// StatementRetryCount counts the number of automatic statement retries that
	// have occurred under READ COMMITTED isolation.
	StatementRetryCount *metric.Counter
}

// EngineMetrics implements the metric.Struct interface.
var _ metric.Struct = EngineMetrics{}

// MetricStruct is part of the metric.Struct interface.
func (EngineMetrics) MetricStruct() {}

// StatsMetrics groups metrics related to SQL Stats collection.
type StatsMetrics struct {
	SQLStatsMemoryMaxBytesHist  metric.IHistogram
	SQLStatsMemoryCurBytesCount *metric.Gauge

	ReportedSQLStatsMemoryMaxBytesHist  metric.IHistogram
	ReportedSQLStatsMemoryCurBytesCount *metric.Gauge

	DiscardedStatsCount *metric.Counter

	SQLStatsFlushesSuccessful       *metric.Counter
	SQLStatsFlushDoneSignalsIgnored *metric.Counter
	SQLStatsFlushFingerprintCount   *metric.Counter
	SQLStatsFlushesFailed           *metric.Counter
	SQLStatsFlushLatency            metric.IHistogram
	SQLStatsRemovedRows             *metric.Counter

	SQLTxnStatsCollectionOverhead metric.IHistogram
}

// StatsMetrics is part of the metric.Struct interface.
var _ metric.Struct = StatsMetrics{}

// MetricStruct is part of the metric.Struct interface.
func (StatsMetrics) MetricStruct() {}

// GuardrailMetrics groups metrics related to different guardrails in the SQL
// layer.
type GuardrailMetrics struct {
	TxnRowsWrittenLogCount *metric.Counter
	TxnRowsWrittenErrCount *metric.Counter
	TxnRowsReadLogCount    *metric.Counter
	TxnRowsReadErrCount    *metric.Counter
}

var _ metric.Struct = GuardrailMetrics{}

// MetricStruct is part of the metric.Struct interface.
func (GuardrailMetrics) MetricStruct() {}

// recordStatementSummary gathers various details pertaining to the
// last executed statement/query and performs the associated
// accounting in the passed-in EngineMetrics.
//   - distSQLUsed reports whether the query was distributed.
//   - automaticRetryTxnCount is the count of implicit txn retries
//     so far.
//   - automaticRetryStmtCount is the count of implicit stmt retries
//     so far.
//   - result is the result set computed by the query/statement.
//   - err is the error encountered, if any.
func (ex *connExecutor) recordStatementSummary(
	ctx context.Context,
	planner *planner,
	automaticRetryTxnCount int,
	automaticRetryStmtCount int,
	rowsAffected int,
	stmtErr error,
	stats topLevelQueryStats,
) appstatspb.StmtFingerprintID {
	phaseTimes := ex.statsCollector.PhaseTimes()

	// Collect the statistics.
	idleLatRaw := phaseTimes.GetIdleLatency(ex.statsCollector.PreviousPhaseTimes())
	idleLatSec := idleLatRaw.Seconds()
	runLatRaw := phaseTimes.GetRunLatency()
	runLatSec := runLatRaw.Seconds()
	parseLatSec := phaseTimes.GetParsingLatency().Seconds()
	planLatSec := phaseTimes.GetPlanningLatency().Seconds()
	// We want to exclude any overhead to reduce possible confusion.
	svcLatRaw := phaseTimes.GetServiceLatencyNoOverhead()
	svcLatSec := svcLatRaw.Seconds()

	// processing latency: contributing towards SQL results.
	processingLatSec := parseLatSec + planLatSec + runLatSec

	// overhead latency: txn/retry management, error checking, etc
	execOverheadSec := svcLatSec - processingLatSec

	stmt := &planner.stmt
	flags := planner.curPlan.flags
	ex.recordStatementLatencyMetrics(
		stmt, flags, automaticRetryTxnCount+automaticRetryStmtCount, runLatRaw, svcLatRaw,
	)

	fullScan := flags.IsSet(planFlagContainsFullIndexScan) || flags.IsSet(planFlagContainsFullTableScan)

	idxRecommendations := idxrecommendations.FormatIdxRecommendations(planner.instrumentation.indexRecs)
	queryLevelStats, queryLevelStatsOk := planner.instrumentation.GetQueryLevelStats()

	var sqlInstanceIDs []int64
	var kvNodeIDs []int32
	if queryLevelStatsOk {
		sqlInstanceIDs = make([]int64, 0, len(queryLevelStats.SQLInstanceIDs))
		for _, sqlInstanceID := range queryLevelStats.SQLInstanceIDs {
			sqlInstanceIDs = append(sqlInstanceIDs, int64(sqlInstanceID))
		}
		kvNodeIDs = queryLevelStats.KVNodeIDs
	}
	startTime := phaseTimes.GetSessionPhaseTime(sessionphase.PlannerStartExecStmt).ToUTC()
	implicitTxn := flags.IsSet(planFlagImplicitTxn)
	stmtFingerprintID := appstatspb.ConstructStatementFingerprintID(
		stmt.StmtNoConstants, implicitTxn, planner.SessionData().Database)
	autoRetryReason := ex.state.mu.autoRetryReason
	if automaticRetryStmtCount > 0 {
		autoRetryReason = planner.autoRetryStmtReason
	}
	recordedStmtStats := &sqlstats.RecordedStmtStats{
		FingerprintID:        stmtFingerprintID,
		QuerySummary:         stmt.StmtSummary,
		Generic:              flags.IsSet(planFlagGeneric),
		DistSQL:              flags.ShouldBeDistributed(),
		Vec:                  flags.IsSet(planFlagVectorized),
		ImplicitTxn:          implicitTxn,
		PlanHash:             planner.instrumentation.planGist.Hash(),
		SessionID:            ex.planner.extendedEvalCtx.SessionID,
		StatementID:          stmt.QueryID,
		AutoRetryCount:       automaticRetryTxnCount + automaticRetryStmtCount,
		Failed:               stmtErr != nil,
		AutoRetryReason:      autoRetryReason,
		RowsAffected:         rowsAffected,
		IdleLatencySec:       idleLatSec,
		ParseLatencySec:      parseLatSec,
		PlanLatencySec:       planLatSec,
		RunLatencySec:        runLatSec,
		ServiceLatencySec:    svcLatSec,
		OverheadLatencySec:   execOverheadSec,
		BytesRead:            stats.bytesRead,
		RowsRead:             stats.rowsRead,
		RowsWritten:          stats.rowsWritten,
		Nodes:                sqlInstanceIDs,
		KVNodeIDs:            kvNodeIDs,
		StatementType:        stmt.AST.StatementType(),
		PlanGist:             planner.instrumentation.planGist.String(),
		StatementError:       stmtErr,
		IndexRecommendations: idxRecommendations,
		Query:                stmt.StmtNoConstants,
		StartTime:            startTime,
		EndTime:              startTime.Add(svcLatRaw),
		FullScan:             fullScan,
		ExecStats:            queryLevelStats,
		// TODO(mgartner): Use a slice of struct{uint64, uint64} instead of
		// converting to strings.
		Indexes:   planner.instrumentation.indexesUsed.Strings(),
		Database:  planner.SessionData().Database,
		QueryTags: stmt.QueryTags,
	}

	err := ex.statsCollector.RecordStatement(ctx, recordedStmtStats)

	if err != nil {
		if log.V(1) {
			log.Warningf(ctx, "failed to record statement: %s", err)
		}
		ex.server.ServerMetrics.StatsMetrics.DiscardedStatsCount.Inc(1)
	}

	// Record statement execution statistics if span is recorded and no error was
	// encountered while collecting query-level statistics.
	if queryLevelStatsOk {
		for _, ev := range queryLevelStats.ContentionEvents {
			if ev.IsLatch && !planner.SessionData().RegisterLatchWaitContentionEvents {
				// This event should be included in the trace and contention time
				// metrics, but not registered with the *_contention_events tables.
				continue
			}
			contentionEvent := contentionpb.ExtendedContentionEvent{
				BlockingEvent:            ev,
				WaitingTxnID:             planner.txn.ID(),
				WaitingStmtFingerprintID: stmtFingerprintID,
				WaitingStmtID:            stmt.QueryID,
				ContentionType:           contentionpb.ContentionType_LOCK_WAIT,
			}

			ex.server.cfg.ContentionRegistry.AddContentionEvent(contentionEvent)
		}

		if queryLevelStats.ContentionTime > 0 {
			dbName := ex.sessionData().Database
			appName := ex.sessionData().ApplicationName
			ex.planner.DistSQLPlanner().distSQLSrv.Metrics.ContendedQueriesCount.Inc(1, dbName, appName)
			ex.planner.DistSQLPlanner().distSQLSrv.Metrics.CumulativeContentionNanos.Inc(queryLevelStats.ContentionTime.Nanoseconds(), dbName, appName)
		}
	}

	// Do some transaction level accounting for the transaction this statement is
	// a part of.

	// We limit the number of statementFingerprintIDs stored for a transaction, as dictated
	// by the TxnStatsNumStmtFingerprintIDsToRecord cluster setting.
	maxStmtFingerprintIDsLen := sqlstats.TxnStatsNumStmtFingerprintIDsToRecord.Get(&ex.server.cfg.Settings.SV)
	if int64(len(ex.extraTxnState.transactionStatementFingerprintIDs)) < maxStmtFingerprintIDsLen {
		ex.extraTxnState.transactionStatementFingerprintIDs = append(
			ex.extraTxnState.transactionStatementFingerprintIDs, stmtFingerprintID)
	}

	// Add the current statement's ID to the hash. We don't track queries issued
	// by the internal executor, in which case the hash is uninitialized, and
	// can therefore be safely ignored.
	if ex.extraTxnState.transactionStatementsHash.IsInitialized() {
		ex.extraTxnState.transactionStatementsHash.Add(uint64(stmtFingerprintID))
	}
	ex.extraTxnState.numRows += rowsAffected
	ex.extraTxnState.idleLatency += idleLatRaw

	if log.V(2) {
		// ages since significant epochs
		sessionAge := phaseTimes.GetSessionAge().Seconds()

		log.Infof(ctx,
			"query stats: %d rows, %d retries, "+
				"parse %.2fµs (%.1f%%), "+
				"plan %.2fµs (%.1f%%), "+
				"run %.2fµs (%.1f%%), "+
				"overhead %.2fµs (%.1f%%), "+
				"session age %.4fs",
			rowsAffected, automaticRetryTxnCount+automaticRetryStmtCount,
			parseLatSec*1e6, 100*parseLatSec/svcLatSec,
			planLatSec*1e6, 100*planLatSec/svcLatSec,
			runLatSec*1e6, 100*runLatSec/svcLatSec,
			execOverheadSec*1e6, 100*execOverheadSec/svcLatSec,
			sessionAge,
		)
	}

	return stmtFingerprintID
}

func (ex *connExecutor) recordStatementLatencyMetrics(
	stmt *Statement,
	flags planFlags,
	automaticRetryCount int,
	runLatRaw time.Duration,
	svcLatRaw time.Duration,
) {
	shouldIncludeInLatencyMetrics := shouldIncludeStmtInLatencyMetrics(stmt)
	if automaticRetryCount == 0 {
		ex.updateOptCounters(flags)
		m := &ex.metrics.EngineMetrics

		m.StatementFingerprintCount.Add([]byte(stmt.StmtNoConstants))

		if flags.ShouldBeDistributed() {
			if _, ok := stmt.AST.(*tree.Select); ok {
				m.DistSQLSelectCount.Inc(1)
				if flags.IsSet(planFlagDistributedExecution) {
					m.DistSQLSelectDistributedCount.Inc(1)
				}
			}
			if shouldIncludeInLatencyMetrics {
				m.DistSQLExecLatency.RecordValue(runLatRaw.Nanoseconds())
				m.DistSQLServiceLatency.RecordValue(svcLatRaw.Nanoseconds())
			}
		}
		if shouldIncludeInLatencyMetrics {
			if detailedLatencyMetrics.Get(&ex.server.cfg.Settings.SV) {
				labels := map[string]string{
					detailedLatencyMetricLabel: stmt.StmtNoConstants,
				}
				m.SQLExecLatencyDetail.Observe(labels, float64(runLatRaw.Nanoseconds()))
			}
			m.SQLExecLatency.RecordValue(runLatRaw.Nanoseconds())
			m.SQLServiceLatency.RecordValue(svcLatRaw.Nanoseconds(), ex.sessionData().Database, ex.sessionData().ApplicationName)
			if ex.state.isHistorical.Load() {
				m.SQLExecLatencyHistorical.RecordValue(runLatRaw.Nanoseconds())
				m.SQLServiceLatencyHistorical.RecordValue(svcLatRaw.Nanoseconds())
			} else {
				m.SQLExecLatencyConsistent.RecordValue(runLatRaw.Nanoseconds())
				m.SQLServiceLatencyConsistent.RecordValue(svcLatRaw.Nanoseconds())
			}
		}
	}
}

func (ex *connExecutor) updateOptCounters(planFlags planFlags) {
	m := &ex.metrics.EngineMetrics

	if planFlags.IsSet(planFlagOptCacheHit) {
		m.SQLOptPlanCacheHits.Inc(1)
	} else if planFlags.IsSet(planFlagOptCacheMiss) {
		m.SQLOptPlanCacheMisses.Inc(1)
	}
}

// We only want to keep track of DML (Data Manipulation Language) statements in our latency metrics.
func shouldIncludeStmtInLatencyMetrics(stmt *Statement) bool {
	return stmt.AST.StatementType() == tree.TypeDML
}
