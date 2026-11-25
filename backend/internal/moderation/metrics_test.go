package moderation

import "testing"

func TestShadowMetricsSeparateRetryTerminalAndHumanWait(t *testing.T) {
	wait := int64(320)
	summary := summarizeShadow([]shadowMetricRow{
		{ResultState: "queued", Failure: "deadline_exceeded", ModelCalls: 1, Tokens: 20, ModelMS: 100, QueueMS: 50},
		{ResultState: "awaiting_review", ModelCalls: 2, Tokens: 90, ModelMS: 240, ReportToDecisionMS: 1000, HumanWaitMS: &wait},
		{ResultState: "resolved", ReportToDecisionMS: 800},
	})
	if summary.Attempts != 3 || summary.Terminal != 2 || summary.AwaitingReview != 1 || summary.Timeouts != 1 || summary.ModelCalls != 3 || summary.Tokens != 110 {
		t.Fatalf("unexpected outcome counters: %+v", summary)
	}
	if summary.Model.Samples != 2 || summary.Model.MeanMS != 170 || summary.Model.P95MS != 240 || summary.ReportToDecision.MeanMS != 900 || summary.HumanWait.P95MS != 320 {
		t.Fatalf("unexpected latency summaries: %+v", summary)
	}
}

func TestShadowDispositionMetricsCountLatestAttemptPerCase(t *testing.T) {
	metrics := summarizeShadow([]shadowMetricRow{
		{CaseID: 1, ResultState: "resolved", ExecutionOutcome: "applied"},
		{CaseID: 1, ResultState: "queued", Failure: "deadline_exceeded"},
		{CaseID: 2, ResultState: "awaiting_review", Failure: "invalid_output"},
		{CaseID: 3, ResultState: "awaiting_review", ExecutionOutcome: "rollout_missing"},
		{CaseID: 4, ResultState: "resolved", ExecutionOutcome: "target_removed"},
	})
	if metrics.Attempts != 5 || metrics.ObservedCases != 4 || metrics.AutoSettledCases != 1 ||
		metrics.ReferredCases != 2 || metrics.FailedReferralCases != 1 ||
		metrics.AutoSettlementRate != 0.25 || metrics.ReferralRate != 0.5 {
		t.Fatalf("unexpected disposition metrics: %+v", metrics)
	}
}
