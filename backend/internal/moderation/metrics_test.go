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
