package moderation

import (
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type latencySummary struct {
	Samples int     `json:"samples"`
	MeanMS  float64 `json:"mean_ms"`
	P95MS   int64   `json:"p95_ms"`
}

func latencySummaryFor(values []int64) latencySummary {
	if len(values) == 0 {
		return latencySummary{}
	}
	slices.Sort(values)
	var total int64
	for _, value := range values {
		total += value
	}
	return latencySummary{Samples: len(values), MeanMS: float64(total) / float64(len(values)), P95MS: values[(len(values)*95+99)/100-1]}
}

type shadowMetricRow struct {
	CaseID                                                       uint
	ExecutionOutcome                                             string
	QueueMS, EvidenceMS, ModelMS, DurationMS, ReportToDecisionMS int64
	ModelCalls, Tokens                                           int
	ResultState, Failure                                         string
	HumanWaitMS                                                  *int64
}

type shadowMetrics struct {
	ObservedCases       int            `json:"observed_cases"`
	AutoSettledCases    int            `json:"auto_settled_cases"`
	ReferredCases       int            `json:"referred_cases"`
	FailedReferralCases int            `json:"failed_referral_cases"`
	AutoSettlementRate  float64        `json:"auto_settlement_rate"`
	ReferralRate        float64        `json:"referral_rate"`
	Attempts            int            `json:"attempts"`
	Terminal            int            `json:"terminal"`
	AwaitingReview      int            `json:"awaiting_review"`
	Timeouts            int            `json:"timeouts"`
	ModelCalls          int            `json:"model_calls"`
	Tokens              int            `json:"tokens"`
	Queue               latencySummary `json:"queue"`
	Evidence            latencySummary `json:"evidence"`
	Model               latencySummary `json:"model"`
	Execution           latencySummary `json:"execution"`
	ReportToDecision    latencySummary `json:"report_to_decision"`
	HumanWait           latencySummary `json:"human_wait"`
}

func summarizeShadow(rows []shadowMetricRow) shadowMetrics {
	output := shadowMetrics{Attempts: len(rows)}
	cases := make(map[uint]shadowMetricRow)
	var queue, evidence, model, execution, endToEnd, wait []int64
	for _, row := range rows {
		if row.CaseID != 0 {
			if _, seen := cases[row.CaseID]; !seen {
				cases[row.CaseID] = row
			}
		}
		queue = append(queue, row.QueueMS)
		evidence = append(evidence, row.EvidenceMS)
		execution = append(execution, row.DurationMS)
		output.ModelCalls += row.ModelCalls
		output.Tokens += row.Tokens
		if row.ModelCalls > 0 {
			model = append(model, row.ModelMS)
		}
		if row.Failure == "deadline_exceeded" {
			output.Timeouts++
		}
		if row.ResultState == "resolved" || row.ResultState == "awaiting_review" {
			output.Terminal++
			endToEnd = append(endToEnd, row.ReportToDecisionMS)
		}
		if row.ResultState == "awaiting_review" {
			output.AwaitingReview++
		}
		if row.HumanWaitMS != nil {
			wait = append(wait, max(0, *row.HumanWaitMS))
		}
	}
	output.ObservedCases = len(cases)
	for _, row := range cases {
		switch row.ResultState {
		case "resolved":
			if row.ExecutionOutcome == "applied" {
				output.AutoSettledCases++
			}
		case "awaiting_review":
			output.ReferredCases++
			if row.Failure != "" {
				output.FailedReferralCases++
			}
		}
	}
	if output.ObservedCases > 0 {
		output.AutoSettlementRate = float64(output.AutoSettledCases) / float64(output.ObservedCases)
		output.ReferralRate = float64(output.ReferredCases) / float64(output.ObservedCases)
	}
	output.Queue = latencySummaryFor(queue)
	output.Evidence = latencySummaryFor(evidence)
	output.Model = latencySummaryFor(model)
	output.Execution = latencySummaryFor(execution)
	output.ReportToDecision = latencySummaryFor(endToEnd)
	output.HumanWait = latencySummaryFor(wait)
	return output
}

func shadowMetricsHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var input struct {
			Model string `json:"model" binding:"required,max=100"`
			Hours int    `json:"hours" binding:"omitempty,min=1,max=168"`
		}
		if c.ShouldBindJSON(&input) != nil || strings.TrimSpace(input.Model) == "" {
			c.AbortWithStatus(400)
			return
		}
		if input.Hours == 0 {
			input.Hours = 24
		}
		until := time.Now().UTC()
		since := until.Add(-time.Duration(input.Hours) * time.Hour)
		rows := []shadowMetricRow{}
		err := db.WithContext(c.Request.Context()).Table("moderation_shadow_runs s").
			Select(`s.case_id,s.execution_outcome,s.queue_ms,s.evidence_ms,s.model_ms,s.duration_ms,s.report_to_decision_ms,s.model_calls,s.tokens,s.result_state,s.failure,
     CASE WHEN s.result_state = 'awaiting_review' THEN
       TIMESTAMPDIFF(MICROSECOND,s.created_at,(SELECT MIN(d.created_at) FROM moderation_decisions d WHERE d.case_id=s.case_id AND d.reviewer_id<>0 AND d.created_at>=s.created_at)) DIV 1000
     ELSE NULL END AS human_wait_ms`).
			Where("s.model = ? AND s.created_at >= ? AND s.created_at <= ? AND s.result_state <> ''", strings.TrimSpace(input.Model), since, until).
			Order("s.created_at DESC, s.id DESC").Limit(10001).Scan(&rows).Error
		if err != nil {
			respondError(c, err)
			return
		}
		truncated := len(rows) > 10000
		if truncated {
			rows = rows[:10000]
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(200, gin.H{"model": strings.TrimSpace(input.Model), "since": since, "until": until, "truncated": truncated, "metrics": summarizeShadow(rows)})
	}
}
