package moderation

import (
	"regexp"
	"strings"
)

const CandidateRuleVersion = "spam-promo-v1"
const CandidateConfidence = 0.95

var (
	homeChannel         = regexp.MustCompile(`(?:群号|渠道)(?:见|在)(?:我|个人)?主页`)
	solicitation        = regexp.MustCompile(`加群|进群|扫码|私信我|联系我|加我微信|点击链接|加入群聊`)
	promotion           = regexp.MustCompile(`返现|领红包|免费领|优惠券|佣金|推广|代理|刷单|日赚`)
	channelMarker       = regexp.MustCompile(`\[(?:链接|号码|联系方式|邮箱)\]|(?:群号|渠道)(?:见|在)(?:我|个人)?主页`)
	contextualException = regexp.MustCompile(`[“”‘’「」『』"'《》]|别信|不要|请勿|骗局揭秘|骗局|骗术|骗子|诈骗|钓鱼|举报|引用|楼上|有人说|他说|她说|转述|提醒|曝光|警惕|反讽|不建议|不要相信`)
)

// FoldCandidate runs the same conservative eligibility rules against the
// desensitized evidence online and in the offline evaluator.
func FoldCandidate(snapshot Snapshot, suggestion Suggestion) bool {
	if snapshot.TargetRemoved || !snapshot.VerifiedChannel || suggestion.Action != "suggest_fold" || suggestion.PolicyID != "SPAM_PROMO" ||
		!suggestion.valid(snapshot.Items) || *suggestion.Confidence < CandidateConfidence {
		return false
	}
	var target Evidence
	hasParent := !snapshot.NeedsContext
	for _, item := range snapshot.Items {
		switch item.ID {
		case "target:1":
			target = item
		case "parent:1":
			if snapshot.NeedsContext {
				hasParent = item.Status == "ok" && containsEvidence(suggestion.EvidenceIDs, item.ID)
			}
		}
	}
	return hasParent && target.Status == "ok" && len([]rune(target.Text)) < 512 &&
		!contextualException.MatchString(target.Text) && solicitation.MatchString(target.Text) &&
		promotion.MatchString(target.Text) && channelMarker.MatchString(target.Text) &&
		!strings.Contains(target.Text, "假")
}

func hasOriginalChannel(text string) bool {
	if homeChannel.MatchString(text) {
		return true
	}
	for _, pattern := range contactPatterns {
		if pattern.pattern.MatchString(text) {
			return true
		}
	}
	return false
}
