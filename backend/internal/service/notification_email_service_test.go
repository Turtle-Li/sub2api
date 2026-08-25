package service

import (
	"bufio"
	"context"
	"errors"
	"io"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNotificationEmailPreviewEscapesHTMLAndSanitizesSubject(t *testing.T) {
	ctx := context.Background()
	svc := NewNotificationEmailService(newNotificationEmailMemorySettingRepo(), nil)

	preview, err := svc.PreviewTemplate(ctx, NotificationEmailPreviewInput{
		Event:   NotificationEmailEventBalanceLow,
		Locale:  "en-US,en;q=0.9",
		Subject: "Low balance for {{recipient_name}}\r\nInjected",
		HTML:    `<p>{{recipient_name}}</p><a href="{{recharge_url}}">Recharge</a>`,
		Variables: map[string]string{
			"recipient_name": `<script>alert("x")</script>`,
			"recharge_url":   `javascript:alert(1)`,
		},
	})
	require.NoError(t, err)
	require.NotContains(t, preview.Subject, "\r")
	require.NotContains(t, preview.Subject, "\n")
	require.Contains(t, preview.Subject, `Low balance for <script>alert("x")</script>Injected`)
	require.Contains(t, preview.HTML, `&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;`)
	require.NotContains(t, preview.HTML, `javascript:alert(1)`)
	require.Contains(t, preview.HTML, `href=""`)
}

func TestNotificationEmailTemplateOverrideAndRestore(t *testing.T) {
	ctx := context.Background()
	repo := newNotificationEmailMemorySettingRepo()
	svc := NewNotificationEmailService(repo, nil)

	official, err := svc.GetTemplate(ctx, NotificationEmailEventBalanceRechargeSuccess, "en")
	require.NoError(t, err)
	require.False(t, official.IsCustom)

	updated, err := svc.UpdateTemplate(
		ctx,
		NotificationEmailEventBalanceRechargeSuccess,
		"zh-Hans",
		"充值完成：{{recharge_amount}}",
		"<p>{{recipient_name}} 已充值 {{recharge_amount}}</p>",
	)
	require.NoError(t, err)
	require.True(t, updated.IsCustom)
	require.Equal(t, "zh", updated.Locale)
	require.Equal(t, "充值完成：{{recharge_amount}}", updated.Subject)
	require.NotNil(t, updated.UpdatedAt)

	restored, err := svc.RestoreOfficialTemplate(ctx, NotificationEmailEventBalanceRechargeSuccess, "zh")
	require.NoError(t, err)
	require.False(t, restored.IsCustom)
	require.NotEqual(t, updated.Subject, restored.Subject)
	_, err = repo.GetValue(ctx, notificationEmailTemplateKey(NotificationEmailEventBalanceRechargeSuccess, "zh"))
	require.ErrorIs(t, err, ErrSettingNotFound)
}

func TestNotificationEmailTemplateRejectsUnsupportedPlaceholder(t *testing.T) {
	ctx := context.Background()
	svc := NewNotificationEmailService(newNotificationEmailMemorySettingRepo(), nil)

	_, err := svc.UpdateTemplate(
		ctx,
		NotificationEmailEventSubscriptionPurchaseSuccess,
		"en",
		"Purchased {{not_allowed}}",
		"<p>{{subscription_group}}</p>",
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported placeholder")
}

func TestNotificationEmailAuthTemplatesAreListedAndPreviewable(t *testing.T) {
	ctx := context.Background()
	svc := NewNotificationEmailService(newNotificationEmailMemorySettingRepo(), nil)

	infos := svc.ListEventInfos()
	events := make(map[string]NotificationEmailEventInfo, len(infos))
	for _, info := range infos {
		events[info.Event] = info
	}
	require.Contains(t, events, NotificationEmailEventAuthVerifyCode)
	require.Contains(t, events, NotificationEmailEventAuthPasswordReset)
	require.False(t, events[NotificationEmailEventAuthVerifyCode].Optional)
	require.False(t, events[NotificationEmailEventAuthPasswordReset].Optional)
	require.Contains(t, events[NotificationEmailEventAuthVerifyCode].Placeholders, "verification_code")
	require.Contains(t, events[NotificationEmailEventAuthPasswordReset].Placeholders, "reset_url")

	verifyPreview, err := svc.PreviewTemplate(ctx, NotificationEmailPreviewInput{
		Event:  NotificationEmailEventAuthVerifyCode,
		Locale: "zh-CN",
		Variables: map[string]string{
			"verification_code":  "654321",
			"expires_in_minutes": "15",
		},
	})
	require.NoError(t, err)
	require.Contains(t, verifyPreview.Subject, "邮箱验证码")
	require.Contains(t, verifyPreview.HTML, "654321")

	resetPreview, err := svc.PreviewTemplate(ctx, NotificationEmailPreviewInput{
		Event:  NotificationEmailEventAuthPasswordReset,
		Locale: "en",
		Variables: map[string]string{
			"reset_url":          "https://example.com/reset?token=abc",
			"expires_in_minutes": "30",
		},
	})
	require.NoError(t, err)
	require.Contains(t, resetPreview.Subject, "Password reset")
	require.Contains(t, resetPreview.HTML, "https://example.com/reset?token=abc")
}

func TestNotificationEmailAdditionalEventsAreListedAndPreviewable(t *testing.T) {
	ctx := context.Background()
	svc := NewNotificationEmailService(newNotificationEmailMemorySettingRepo(), nil)

	infos := svc.ListEventInfos()
	events := make(map[string]NotificationEmailEventInfo, len(infos))
	for _, info := range infos {
		events[info.Event] = info
	}

	checks := []struct {
		event       string
		placeholder string
	}{
		{NotificationEmailEventNotificationEmailVerifyCode, "verification_code"},
		{NotificationEmailEventAccountQuotaAlert, "account_name"},
		{NotificationEmailEventContentModerationViolation, "moderation_category"},
		{NotificationEmailEventContentModerationDisabled, "violation_count"},
		{NotificationEmailEventCyberPolicyNotice, "upstream_message"},
		{NotificationEmailEventOpsAlert, "rule_name"},
		{NotificationEmailEventOpsScheduledReport, "report_html"},
	}

	for _, check := range checks {
		info, ok := events[check.event]
		require.Truef(t, ok, "expected %s to be listed", check.event)
		require.False(t, info.Optional)
		require.Contains(t, info.Placeholders, check.placeholder)

		preview, err := svc.PreviewTemplate(ctx, NotificationEmailPreviewInput{Event: check.event, Locale: "zh"})
		require.NoError(t, err)
		require.NotEmpty(t, preview.Subject)
		require.NotEmpty(t, preview.HTML)
	}
}

func TestCyberPolicyNoticeTemplateWrapsLongUpstreamMessages(t *testing.T) {
	ctx := context.Background()
	svc := NewNotificationEmailService(newNotificationEmailMemorySettingRepo(), nil)
	longMessage := strings.Repeat("0123456789abcdef", 256)

	for _, locale := range []string{"en", "zh"} {
		preview, err := svc.PreviewTemplate(ctx, NotificationEmailPreviewInput{
			Event:  NotificationEmailEventCyberPolicyNotice,
			Locale: locale,
			Variables: map[string]string{
				"upstream_message": longMessage,
			},
		})
		require.NoError(t, err)
		require.Contains(t, preview.HTML, "table-layout:fixed")
		require.Contains(t, preview.HTML, "overflow-wrap:anywhere")
		require.Contains(t, preview.HTML, "word-break:break-all")
		require.NotContains(t, preview.HTML, "{{upstream_message}}")
	}
}

func TestOpsScheduledReportTemplateExposesEditableSummaryMetrics(t *testing.T) {
	ctx := context.Background()
	svc := NewNotificationEmailService(newNotificationEmailMemorySettingRepo(), nil)

	requiredPlaceholders := []string{
		"report_summary_display",
		"report_detail_display",
		"report_total_requests",
		"report_success_count",
		"report_sla_error_count",
		"report_business_limited_count",
		"report_sla",
		"report_error_rate",
		"report_upstream_error_rate",
		"report_upstream_error_count_excl_429_529",
		"report_upstream_429_count",
		"report_upstream_529_count",
		"report_latency_p50",
		"report_latency_p99",
		"report_ttft_p50",
		"report_ttft_p99",
		"report_tokens",
		"report_qps_current",
		"report_qps_peak",
		"report_qps_avg",
		"report_tps_current",
		"report_tps_peak",
		"report_tps_avg",
	}

	for _, locale := range []string{"en", "zh"} {
		tmpl, err := svc.GetTemplate(ctx, NotificationEmailEventOpsScheduledReport, locale)
		require.NoError(t, err)
		for _, placeholder := range requiredPlaceholders {
			require.Contains(t, tmpl.Placeholders, placeholder)
			require.Contains(t, tmpl.HTML, "{{"+placeholder+"}}")
		}

		preview, err := svc.PreviewTemplate(ctx, NotificationEmailPreviewInput{
			Event:  NotificationEmailEventOpsScheduledReport,
			Locale: locale,
		})
		require.NoError(t, err)
		require.Contains(t, preview.HTML, "2,374")
		require.Contains(t, preview.HTML, "99.86%")
		require.Contains(t, preview.HTML, "151,260 ms")
		require.Contains(t, preview.HTML, `style="display: none;"`)
		require.NotContains(t, preview.HTML, "{{report_total_requests}}")
	}
}

func TestOpsScheduledReportRuntimeVariablesDoNotLeakPreviewSamples(t *testing.T) {
	ctx := context.Background()
	svc := NewNotificationEmailService(newNotificationEmailMemorySettingRepo(), nil)

	variables := svc.runtimeVariables(ctx, NotificationEmailEventOpsScheduledReport, "en", NotificationEmailSendInput{})
	require.Equal(t, "none", variables["report_summary_display"])
	require.Equal(t, "block", variables["report_detail_display"])
	require.Empty(t, variables["report_html"])
	for _, placeholder := range notificationEmailOpsSummaryPlaceholders {
		if placeholder == "report_summary_display" {
			continue
		}
		require.Equal(t, "-", variables[placeholder])
	}

	rendered, err := renderNotificationEmail(
		NotificationEmailEventOpsScheduledReport,
		"Report",
		`<div style="display: {{report_detail_display}};">{{report_html}}</div>`,
		variables,
		nil,
	)
	require.NoError(t, err)
	require.NotContains(t, rendered.HTML, "<h2>Daily summary</h2>")
}

func TestNotificationEmailRawHTMLVariablesAreTrustedOnlyForHTMLPlaceholders(t *testing.T) {
	require.True(t, notificationEmailRawHTMLAllowed(NotificationEmailEventOpsScheduledReport, "report_html"))
	require.False(t, notificationEmailRawHTMLAllowed(NotificationEmailEventOpsScheduledReport, "recipient_name"))
	require.False(t, notificationEmailRawHTMLAllowed(NotificationEmailEventOpsAlert, "report_html"))

	preview, err := renderNotificationEmail(
		NotificationEmailEventOpsScheduledReport,
		"Report for {{recipient_name}}",
		`<section>{{report_html}}</section><p>{{recipient_name}}</p>`,
		map[string]string{
			"recipient_name": `<script>alert("x")</script>`,
			"report_html":    `<p>escaped report</p>`,
		},
		map[string]string{
			"report_html": `<table><tr><td>trusted report</td></tr></table>`,
		},
	)
	require.NoError(t, err)
	require.Contains(t, preview.HTML, `<table><tr><td>trusted report</td></tr></table>`)
	require.NotContains(t, preview.HTML, `escaped report`)
	require.Contains(t, preview.HTML, `&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;`)
	require.Contains(t, preview.Subject, `<script>alert("x")</script>`)

	preview, err = renderNotificationEmail(
		NotificationEmailEventOpsScheduledReport,
		"Recipient {{recipient_name}}",
		`<p>{{recipient_name}}</p>`,
		map[string]string{"recipient_name": `<em>escaped</em>`},
		map[string]string{"recipient_name": `<strong>raw</strong>`},
	)
	require.NoError(t, err)
	require.Contains(t, preview.HTML, `&lt;em&gt;escaped&lt;/em&gt;`)
	require.NotContains(t, preview.HTML, `<strong>raw</strong>`)
}

func TestNotificationEmailFallbackClassification(t *testing.T) {
	templateErr := notificationEmailTemplateErr(errors.New("bad template"))
	configErr := notificationEmailConfigErr(errors.New("missing email service"))
	deliveryErr := notificationEmailDeliveryErr(errors.New("smtp timeout"))

	require.True(t, shouldFallbackNotificationEmail(templateErr))
	require.True(t, shouldFallbackNotificationEmail(configErr))
	require.False(t, shouldFallbackNotificationEmail(deliveryErr))
	require.True(t, isNotificationEmailDeliveryError(deliveryErr))
	require.False(t, isNotificationEmailDeliveryError(templateErr))
	require.False(t, shouldFallbackNotificationEmail(nil))
}

func TestEmailQueueTasksPreserveLocaleHints(t *testing.T) {
	queue := &EmailQueueService{taskChan: make(chan EmailTask, 3)}
	require.NoError(t, queue.EnqueueVerifyCode("user@example.com", "Sub2API", "zh-CN"))
	require.NoError(t, queue.EnqueueVerifyCodeForUser("member@example.com", "Sub2API", 42, "en-US"))
	require.NoError(t, queue.EnqueuePasswordReset("user@example.com", "Sub2API", "https://example.com/reset", "en-US"))

	verifyTask := <-queue.taskChan
	require.Equal(t, TaskTypeVerifyCode, verifyTask.TaskType)
	require.Equal(t, "zh-CN", verifyTask.Locale)
	require.Zero(t, verifyTask.UserID)

	authenticatedVerifyTask := <-queue.taskChan
	require.Equal(t, TaskTypeVerifyCode, authenticatedVerifyTask.TaskType)
	require.Equal(t, "en-US", authenticatedVerifyTask.Locale)
	require.Equal(t, int64(42), authenticatedVerifyTask.UserID)

	resetTask := <-queue.taskChan
	require.Equal(t, TaskTypePasswordReset, resetTask.TaskType)
	require.Equal(t, "en-US", resetTask.Locale)
	require.Zero(t, resetTask.UserID)
}

func TestOpsScheduledReportDeliverySourceIDIncludesReportIdentity(t *testing.T) {
	report := &opsScheduledReport{Name: "日报", ReportType: "daily_summary", Schedule: "0 9 * * *"}
	sourceID := opsScheduledReportDeliverySourceID(report)
	require.Contains(t, sourceID, "daily_summary")
	require.Contains(t, sourceID, "日报")
	require.Contains(t, sourceID, "0 9 * * *")
	require.NotEqual(t, sourceID, opsScheduledReportDeliverySourceID(&opsScheduledReport{Name: "周报", ReportType: "weekly_summary", Schedule: "0 9 * * 1"}))
	require.Equal(t, "scheduled_report", opsScheduledReportDeliverySourceID(nil))
}

func TestNotificationEmailUnsubscribeOnlyAllowsOptionalEvents(t *testing.T) {
	ctx := context.Background()
	svc := NewNotificationEmailService(newNotificationEmailMemorySettingRepo(), nil)

	token, err := svc.createUnsubscribeToken(ctx, "User@Example.com", NotificationEmailEventBalanceLow)
	require.NoError(t, err)
	result, err := svc.Unsubscribe(ctx, token)
	require.NoError(t, err)
	require.True(t, result.Done)
	require.Equal(t, NotificationEmailEventBalanceLow, result.Event)
	unsubscribed, err := svc.IsUnsubscribed(ctx, "user@example.com", NotificationEmailEventBalanceLow)
	require.NoError(t, err)
	require.True(t, unsubscribed)

	transactionalToken, err := svc.createUnsubscribeToken(ctx, "user@example.com", NotificationEmailEventBalanceRechargeSuccess)
	require.NoError(t, err)
	_, err = svc.Unsubscribe(ctx, transactionalToken)
	require.Error(t, err)
	require.Contains(t, err.Error(), "transactional")

	authToken, err := svc.createUnsubscribeToken(ctx, "user@example.com", NotificationEmailEventAuthVerifyCode)
	require.NoError(t, err)
	_, err = svc.Unsubscribe(ctx, authToken)
	require.Error(t, err)
	require.Contains(t, err.Error(), "transactional")
}

func TestNotificationEmailLocaleMemoryNormalizesAcceptLanguage(t *testing.T) {
	ctx := context.Background()
	svc := NewNotificationEmailService(newNotificationEmailMemorySettingRepo(), nil)

	svc.RememberRecipientLocale(ctx, 42, "User@Example.com", "zh-CN,zh;q=0.9,en;q=0.8")
	require.Equal(t, "zh", svc.ResolveRecipientLocale(ctx, 42, "user@example.com"))
	require.Equal(t, "zh", svc.ResolveRecipientLocale(ctx, 0, "user@example.com"))
}

func TestNotificationEmailLocaleMemoryRejectsUnauthenticatedRecipients(t *testing.T) {
	ctx := context.Background()
	repo := newNotificationEmailMemorySettingRepo()
	svc := NewNotificationEmailService(repo, nil)
	recipient := "anonymous@example.com"

	svc.RememberRecipientLocale(ctx, 0, recipient, "en-US")
	svc.RememberRecipientLocale(ctx, -1, recipient, "en-US")

	_, err := repo.GetValue(ctx, notificationEmailLocaleEmailKeyPrefix+notificationEmailHash(recipient))
	require.ErrorIs(t, err, ErrSettingNotFound)
	require.Equal(t, notificationEmailLocaleChinese, svc.ResolveRecipientLocale(ctx, 0, recipient))
}

func TestEmailServiceAuthenticatedVerifyCodePersistsLocale(t *testing.T) {
	ctx := context.Background()
	repo := newNotificationEmailMemorySettingRepo()
	smtpServer := startNotificationEmailTestSMTPServer(t)
	require.NoError(t, repo.SetMultiple(ctx, smtpServer.settings()))

	emailSvc := NewEmailService(repo, &notificationEmailCacheStub{})
	svc := NewNotificationEmailService(repo, emailSvc)
	recipient := "member@example.com"

	require.NoError(t, emailSvc.SendVerifyCodeForUser(ctx, recipient, "Sub2API", 42, "en-US"))
	require.Equal(t, notificationEmailLocaleEnglish, svc.ResolveRecipientLocale(ctx, 42, recipient))
	require.Contains(t, smtpServer.lastMessageBody(t), "Email verification code")
}

func TestAuthEmailBindingVerifyCodePersistsAuthenticatedLocale(t *testing.T) {
	ctx := context.Background()
	repo := newNotificationEmailMemorySettingRepo()
	smtpServer := startNotificationEmailTestSMTPServer(t)
	require.NoError(t, repo.SetMultiple(ctx, smtpServer.settings()))

	emailSvc := NewEmailService(repo, &notificationEmailCacheStub{})
	notificationSvc := NewNotificationEmailService(repo, emailSvc)
	user := &User{ID: 42, Email: "existing@example.com"}
	authSvc := &AuthService{
		userRepo:     &notificationEmailUserRepoStub{user: user},
		emailService: emailSvc,
	}

	require.NoError(t, authSvc.SendEmailIdentityBindCode(ctx, user.ID, "member@example.com", "en-US"))
	require.Equal(t, notificationEmailLocaleEnglish, notificationSvc.ResolveRecipientLocale(ctx, user.ID, "member@example.com"))
	require.Contains(t, smtpServer.lastMessageBody(t), "Email verification code")
}

func TestTotpVerifyCodePersistsLocaleForLaterSystemEmail(t *testing.T) {
	ctx := context.Background()
	repo := newNotificationEmailMemorySettingRepo()
	smtpServer := startNotificationEmailTestSMTPServer(t)
	require.NoError(t, repo.SetMultiple(ctx, smtpServer.settings()))
	require.NoError(t, repo.Set(ctx, SettingKeyEmailVerifyEnabled, "true"))

	emailSvc := NewEmailService(repo, &notificationEmailCacheStub{})
	notificationSvc := NewNotificationEmailService(repo, emailSvc)
	queue := NewEmailQueueService(emailSvc, 1)
	t.Cleanup(queue.Stop)
	user := &User{ID: 42, Email: "member@example.com", Role: RoleUser}
	totpSvc := NewTotpService(
		&notificationEmailUserRepoStub{user: user},
		nil,
		nil,
		NewSettingService(repo, nil),
		emailSvc,
		queue,
	)

	require.NoError(t, totpSvc.SendVerifyCode(ctx, user.ID, "en-US"))
	require.Eventually(t, func() bool {
		return smtpServer.messageCount() == 1 &&
			notificationSvc.ResolveRecipientLocale(ctx, user.ID, user.Email) == notificationEmailLocaleEnglish
	}, 3*time.Second, 10*time.Millisecond)

	require.NoError(t, notificationSvc.Send(ctx, NotificationEmailSendInput{
		Event:          NotificationEmailEventSubscriptionExpiryReminder,
		RecipientEmail: user.Email,
		RecipientName:  "Member",
		UserID:         user.ID,
		Variables: map[string]string{
			"subscription_group": "Codex",
			"expiry_time":        "2026-08-16 12:00",
			"days_remaining":     "7",
		},
	}))
	require.Contains(t, smtpServer.lastMessageBody(t), "Subscription expiry reminder")
}

func TestNotificationEmailLocaleFallbackDefaultsToChinese(t *testing.T) {
	ctx := context.Background()
	svc := NewNotificationEmailService(newNotificationEmailMemorySettingRepo(), nil)

	require.Equal(t, notificationEmailLocaleChinese, normalizeNotificationLocale(""))
	require.Equal(t, notificationEmailLocaleChinese, normalizeNotificationLocale("ja-JP"))
	require.Equal(t, notificationEmailLocaleChinese, svc.ResolveRecipientLocale(ctx, 0, "unknown@example.com"))
	require.Equal(t, notificationEmailLocaleChinese, svc.ResolveRecipientLocale(ctx, 42, "unknown@example.com"))
	require.Equal(t, notificationEmailLocaleChinese, NewNotificationEmailService(nil, nil).ResolveRecipientLocale(ctx, 42, "unknown@example.com"))
	require.Equal(t, notificationEmailLocaleEnglish, normalizeNotificationLocale("en-US,en;q=0.9"))
	require.Equal(t, notificationEmailLocaleChinese, normalizeNotificationLocale("zh-Hans-CN"))
}

func TestNotificationEmailSendRemembersAuthenticatedExplicitLocale(t *testing.T) {
	ctx := context.Background()
	repo := newNotificationEmailMemorySettingRepo()
	smtpServer := startNotificationEmailTestSMTPServer(t)
	require.NoError(t, repo.SetMultiple(ctx, smtpServer.settings()))

	emailSvc := NewEmailService(repo, nil)
	svc := NewNotificationEmailService(repo, emailSvc)
	recipient := "User@Example.com"
	userID := int64(42)

	require.NoError(t, svc.Send(ctx, NotificationEmailSendInput{
		Event:          NotificationEmailEventBalanceRechargeSuccess,
		Locale:         "en-US",
		RecipientEmail: recipient,
		RecipientName:  "User",
		UserID:         userID,
		Variables: map[string]string{
			"recharge_amount": "10.00",
			"current_balance": "25.00",
			"order_id":        "1001",
		},
	}))
	userLocale, err := repo.GetValue(ctx, notificationEmailLocaleUserKeyPrefix+"42")
	require.NoError(t, err)
	require.Equal(t, notificationEmailLocaleEnglish, userLocale)
	emailLocale, err := repo.GetValue(ctx, notificationEmailLocaleEmailKeyPrefix+notificationEmailHash(recipient))
	require.NoError(t, err)
	require.Equal(t, notificationEmailLocaleEnglish, emailLocale)

	require.NoError(t, svc.Send(ctx, NotificationEmailSendInput{
		Event:          NotificationEmailEventSubscriptionExpiryReminder,
		RecipientEmail: recipient,
		RecipientName:  "User",
		UserID:         userID,
		Variables: map[string]string{
			"subscription_group": "Codex",
			"expiry_time":        "2026-08-16 12:00",
			"days_remaining":     "7",
		},
	}))
	require.Contains(t, smtpServer.lastMessageBody(t), "Subscription expiry reminder")

	require.NoError(t, svc.Send(ctx, NotificationEmailSendInput{
		Event:          NotificationEmailEventBalanceRechargeSuccess,
		Locale:         "zh-CN",
		RecipientEmail: recipient,
		RecipientName:  "User",
		UserID:         userID,
		Variables: map[string]string{
			"recharge_amount": "10.00",
			"current_balance": "35.00",
			"order_id":        "1002",
		},
	}))
	userLocale, err = repo.GetValue(ctx, notificationEmailLocaleUserKeyPrefix+"42")
	require.NoError(t, err)
	require.Equal(t, notificationEmailLocaleChinese, userLocale)

	require.NoError(t, svc.Send(ctx, NotificationEmailSendInput{
		Event:          NotificationEmailEventSubscriptionExpiryReminder,
		RecipientEmail: recipient,
		RecipientName:  "User",
		UserID:         userID,
		Variables: map[string]string{
			"subscription_group": "Codex",
			"expiry_time":        "2026-08-16 12:00",
			"days_remaining":     "7",
		},
	}))
	require.Contains(t, smtpServer.lastMessageBody(t), "订阅到期提醒")
}

func TestNotificationEmailSendDoesNotRememberUnauthenticatedExplicitLocale(t *testing.T) {
	ctx := context.Background()
	repo := newNotificationEmailMemorySettingRepo()
	smtpServer := startNotificationEmailTestSMTPServer(t)
	require.NoError(t, repo.SetMultiple(ctx, smtpServer.settings()))

	emailSvc := NewEmailService(repo, nil)
	svc := NewNotificationEmailService(repo, emailSvc)
	recipient := "unknown@example.com"
	require.NoError(t, svc.Send(ctx, NotificationEmailSendInput{
		Event:          NotificationEmailEventAuthVerifyCode,
		Locale:         "en-US",
		RecipientEmail: recipient,
		Variables: map[string]string{
			"verification_code":  "123456",
			"expires_in_minutes": "15",
		},
	}))

	_, err := repo.GetValue(ctx, notificationEmailLocaleEmailKeyPrefix+notificationEmailHash(recipient))
	require.ErrorIs(t, err, ErrSettingNotFound)
	require.Equal(t, notificationEmailLocaleChinese, svc.ResolveRecipientLocale(ctx, 0, recipient))
}

func TestOfficialNotificationEmailTemplatesUseBilingualBrandLayout(t *testing.T) {
	ctx := context.Background()
	svc := NewNotificationEmailService(newNotificationEmailMemorySettingRepo(), nil)

	for _, event := range notificationEmailEventOrder {
		for _, locale := range []string{notificationEmailLocaleEnglish, notificationEmailLocaleChinese} {
			t.Run(event+"/"+locale, func(t *testing.T) {
				tmpl, err := svc.GetTemplate(ctx, event, locale)
				require.NoError(t, err)
				require.False(t, tmpl.IsCustom)
				require.Contains(t, tmpl.HTML, "background:#07111f")
				require.Contains(t, tmpl.HTML, "linear-gradient(135deg")
				require.Contains(t, tmpl.HTML, "border-radius:22px")
				require.Contains(t, tmpl.HTML, "{{site_name}}")
				require.NotContains(t, tmpl.HTML, "background: #f4f4f5")

				preview, err := svc.PreviewTemplate(ctx, NotificationEmailPreviewInput{Event: event, Locale: locale})
				require.NoError(t, err)
				require.Contains(t, preview.HTML, "background:#07111f")
				require.NotContains(t, preview.HTML, "{{site_name}}")
			})
		}
	}
}

func TestNotificationEmailDeliveryKeyUsesShortStableHash(t *testing.T) {
	key := notificationEmailDeliveryKey(
		NotificationEmailEventSubscriptionExpiryReminder,
		"user_subscription",
		"1234567890",
		"User@Example.com",
		"7d",
	)
	require.NotEmpty(t, key)
	require.LessOrEqual(t, len(key), 100)
	require.True(t, strings.HasPrefix(key, notificationEmailDeliveryKeyPrefix+"v2:"))
	require.Equal(t, key, notificationEmailDeliveryKey(
		NotificationEmailEventSubscriptionExpiryReminder,
		"user_subscription",
		"1234567890",
		"user@example.com",
		"7d",
	))
	require.NotEqual(t, key, notificationEmailDeliveryKey(
		NotificationEmailEventSubscriptionExpiryReminder,
		"user_subscription",
		"1234567890",
		"user@example.com",
		"3d",
	))

	legacyKey := legacyNotificationEmailDeliveryKey(
		NotificationEmailEventSubscriptionExpiryReminder,
		"user_subscription",
		"1234567890",
		"user@example.com",
		"7d",
	)
	require.Greater(t, len(legacyKey), 100)
}

func TestNotificationEmailPreferenceKeyUsesShortStableHashAndReadsLegacyKey(t *testing.T) {
	ctx := context.Background()
	repo := newNotificationEmailMemorySettingRepo()
	svc := NewNotificationEmailService(repo, nil)

	key := notificationEmailPreferenceKey(NotificationEmailEventSubscriptionExpiryReminder, "User@Example.com")
	require.NotEmpty(t, key)
	require.LessOrEqual(t, len(key), 100)
	require.True(t, strings.HasPrefix(key, notificationEmailPreferenceKeyPrefix+"v2:"))
	require.Equal(t, key, notificationEmailPreferenceKey(NotificationEmailEventSubscriptionExpiryReminder, "user@example.com"))

	legacyKey := legacyNotificationEmailPreferenceKey(NotificationEmailEventSubscriptionExpiryReminder, "user@example.com")
	require.Greater(t, len(legacyKey), 100)
	require.NoError(t, repo.Set(ctx, legacyKey, "unsubscribed"))

	unsubscribed, err := svc.IsUnsubscribed(ctx, "User@Example.com", NotificationEmailEventSubscriptionExpiryReminder)
	require.NoError(t, err)
	require.True(t, unsubscribed)
}

func TestNotificationEmailSendDeduplicatesSubscriptionExpiryReminder(t *testing.T) {
	ctx := context.Background()
	repo := newNotificationEmailMemorySettingRepo()
	smtpServer := startNotificationEmailTestSMTPServer(t)
	require.NoError(t, repo.SetMultiple(ctx, smtpServer.settings()))

	emailSvc := NewEmailService(repo, nil)
	svc := NewNotificationEmailService(repo, emailSvc)
	input := NotificationEmailSendInput{
		Event:          NotificationEmailEventSubscriptionExpiryReminder,
		RecipientEmail: "User@Example.com",
		RecipientName:  "User",
		UserID:         42,
		SourceType:     "user_subscription",
		SourceID:       "1234567890",
		ReminderKey:    "7d",
		Variables: map[string]string{
			"subscription_group": "Codex",
			"expiry_time":        "2026-05-27 12:00",
			"days_remaining":     "7",
		},
	}

	require.NoError(t, svc.Send(ctx, input))
	require.Equal(t, int64(1), smtpServer.messageCount())

	key := notificationEmailDeliveryKey(input.Event, input.SourceType, input.SourceID, input.RecipientEmail, input.ReminderKey)
	require.LessOrEqual(t, len(key), 100)
	_, err := repo.GetValue(ctx, key)
	require.NoError(t, err)

	require.NoError(t, svc.Send(ctx, input))
	require.Equal(t, int64(1), smtpServer.messageCount())
}

func TestNotificationEmailSendRespectsLegacyDeliveryKey(t *testing.T) {
	ctx := context.Background()
	repo := newNotificationEmailMemorySettingRepo()
	svc := NewNotificationEmailService(repo, nil)
	input := NotificationEmailSendInput{
		Event:          NotificationEmailEventSubscriptionExpiryReminder,
		RecipientEmail: "user@example.com",
		SourceType:     "user_subscription",
		SourceID:       "1234567890",
		ReminderKey:    "7d",
	}
	legacyKey := legacyNotificationEmailDeliveryKey(input.Event, input.SourceType, input.SourceID, input.RecipientEmail, input.ReminderKey)
	require.NoError(t, repo.Set(ctx, legacyKey, "sent"))

	require.NoError(t, svc.Send(ctx, input))
}

type notificationEmailMemorySettingRepo struct {
	mu     sync.RWMutex
	values map[string]string
}

type notificationEmailCacheStub struct{}

type notificationEmailUserRepoStub struct {
	UserRepository
	user *User
}

func (r *notificationEmailUserRepoStub) GetByID(_ context.Context, id int64) (*User, error) {
	if r.user == nil || r.user.ID != id {
		return nil, ErrUserNotFound
	}
	return r.user, nil
}

func (r *notificationEmailUserRepoStub) GetByEmail(_ context.Context, email string) (*User, error) {
	if r.user == nil || !strings.EqualFold(r.user.Email, email) {
		return nil, ErrUserNotFound
	}
	return r.user, nil
}

func (r *notificationEmailUserRepoStub) ExistsByEmailAlias(_ context.Context, email string) (bool, error) {
	return r.user != nil && NormalizeEmailForAliasDedup(r.user.Email) == NormalizeEmailForAliasDedup(email), nil
}

func (*notificationEmailCacheStub) GetVerificationCode(context.Context, string) (*VerificationCodeData, error) {
	return nil, nil
}

func (*notificationEmailCacheStub) SetVerificationCode(context.Context, string, *VerificationCodeData, time.Duration) error {
	return nil
}

func (*notificationEmailCacheStub) DeleteVerificationCode(context.Context, string) error { return nil }

func (*notificationEmailCacheStub) GetNotifyVerifyCode(context.Context, string) (*VerificationCodeData, error) {
	return nil, nil
}

func (*notificationEmailCacheStub) SetNotifyVerifyCode(context.Context, string, *VerificationCodeData, time.Duration) error {
	return nil
}

func (*notificationEmailCacheStub) DeleteNotifyVerifyCode(context.Context, string) error { return nil }

func (*notificationEmailCacheStub) GetPasswordResetToken(context.Context, string) (*PasswordResetTokenData, error) {
	return nil, nil
}

func (*notificationEmailCacheStub) SetPasswordResetToken(context.Context, string, *PasswordResetTokenData, time.Duration) error {
	return nil
}

func (*notificationEmailCacheStub) DeletePasswordResetToken(context.Context, string) error {
	return nil
}
func (*notificationEmailCacheStub) IsPasswordResetEmailInCooldown(context.Context, string) bool {
	return false
}

func (*notificationEmailCacheStub) SetPasswordResetEmailCooldown(context.Context, string, time.Duration) error {
	return nil
}

func (*notificationEmailCacheStub) IncrNotifyCodeUserRate(context.Context, int64, time.Duration) (int64, error) {
	return 0, nil
}

func (*notificationEmailCacheStub) GetNotifyCodeUserRate(context.Context, int64) (int64, error) {
	return 0, nil
}

func newNotificationEmailMemorySettingRepo() *notificationEmailMemorySettingRepo {
	return &notificationEmailMemorySettingRepo{values: make(map[string]string)}
}

func (r *notificationEmailMemorySettingRepo) Get(_ context.Context, key string) (*Setting, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.values[key]
	if !ok {
		return nil, ErrSettingNotFound
	}
	return &Setting{Key: key, Value: value}, nil
}

func (r *notificationEmailMemorySettingRepo) GetValue(ctx context.Context, key string) (string, error) {
	setting, err := r.Get(ctx, key)
	if err != nil {
		return "", err
	}
	return setting.Value, nil
}

func (r *notificationEmailMemorySettingRepo) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values[key] = value
	return nil
}

func (r *notificationEmailMemorySettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (r *notificationEmailMemorySettingRepo) SetMultiple(_ context.Context, settings map[string]string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, value := range settings {
		r.values[key] = value
	}
	return nil
}

func (r *notificationEmailMemorySettingRepo) GetAll(_ context.Context) (map[string]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]string, len(r.values))
	for key, value := range r.values {
		out[key] = value
	}
	return out, nil
}

func (r *notificationEmailMemorySettingRepo) Delete(_ context.Context, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.values[key]; !ok {
		return ErrSettingNotFound
	}
	delete(r.values, key)
	return nil
}

func TestNotificationEmailMemorySettingRepoSatisfiesInterface(t *testing.T) {
	var _ SettingRepository = (*notificationEmailMemorySettingRepo)(nil)
	require.False(t, strings.Contains(notificationEmailPreferenceKey(NotificationEmailEventBalanceLow, "User@Example.com"), "User@Example.com"))
}

type notificationEmailTestSMTPServer struct {
	listener      net.Listener
	wg            sync.WaitGroup
	messages      atomic.Int64
	messageMu     sync.Mutex
	messageBodies []string
}

func startNotificationEmailTestSMTPServer(t *testing.T) *notificationEmailTestSMTPServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server := &notificationEmailTestSMTPServer{listener: listener}
	server.wg.Add(1)
	go server.serve()
	t.Cleanup(server.close)
	return server
}

func (s *notificationEmailTestSMTPServer) settings() map[string]string {
	host, port, _ := net.SplitHostPort(s.listener.Addr().String())
	return map[string]string{
		SettingKeySMTPHost:     host,
		SettingKeySMTPPort:     port,
		SettingKeySMTPUsername: "user",
		SettingKeySMTPPassword: "password",
		SettingKeySMTPFrom:     "noreply@example.com",
		SettingKeySMTPFromName: "Sub2API",
		SettingKeySMTPUseTLS:   "false",
	}
}

func (s *notificationEmailTestSMTPServer) messageCount() int64 {
	return s.messages.Load()
}

func (s *notificationEmailTestSMTPServer) lastMessage() string {
	s.messageMu.Lock()
	defer s.messageMu.Unlock()
	if len(s.messageBodies) == 0 {
		return ""
	}
	return s.messageBodies[len(s.messageBodies)-1]
}

func (s *notificationEmailTestSMTPServer) lastMessageBody(t *testing.T) string {
	t.Helper()

	message, err := mail.ReadMessage(strings.NewReader(s.lastMessage()))
	require.NoError(t, err)

	bodyReader := io.Reader(message.Body)
	if strings.EqualFold(message.Header.Get("Content-Transfer-Encoding"), "quoted-printable") {
		bodyReader = quotedprintable.NewReader(message.Body)
	}
	body, err := io.ReadAll(bodyReader)
	require.NoError(t, err)
	return string(body)
}

func (s *notificationEmailTestSMTPServer) close() {
	_ = s.listener.Close()
	s.wg.Wait()
}

func (s *notificationEmailTestSMTPServer) serve() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.handleConn(conn)
	}
}

func (s *notificationEmailTestSMTPServer) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	writeLine := func(line string) bool {
		if _, err := rw.WriteString(line + "\r\n"); err != nil {
			return false
		}
		return rw.Flush() == nil
	}
	if !writeLine("220 localhost ESMTP") {
		return
	}
	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimRight(line, "\r\n"))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			if _, err := rw.WriteString("250-localhost\r\n250 AUTH PLAIN\r\n"); err != nil {
				return
			}
			if err := rw.Flush(); err != nil {
				return
			}
		case strings.HasPrefix(cmd, "AUTH"):
			if !writeLine("235 2.7.0 Authentication successful") {
				return
			}
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			if !writeLine("250 2.1.0 OK") {
				return
			}
		case strings.HasPrefix(cmd, "RCPT TO:"):
			if !writeLine("250 2.1.5 OK") {
				return
			}
		case strings.HasPrefix(cmd, "DATA"):
			if !writeLine("354 End data with <CR><LF>.<CR><LF>") {
				return
			}
			var message strings.Builder
			for {
				dataLine, err := rw.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(dataLine, "\r\n") == "." {
					break
				}
				_, _ = message.WriteString(dataLine)
			}
			s.messageMu.Lock()
			s.messageBodies = append(s.messageBodies, message.String())
			s.messageMu.Unlock()
			s.messages.Add(1)
			if !writeLine("250 2.0.0 OK") {
				return
			}
		case strings.HasPrefix(cmd, "QUIT"):
			_ = writeLine("221 2.0.0 Bye")
			return
		default:
			if !writeLine("250 OK") {
				return
			}
		}
	}
}
