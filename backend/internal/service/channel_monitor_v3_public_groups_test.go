package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV3GroupHistoryMatchesTenMinuteChecks(t *testing.T) {
	r := &monitorV3RepoFake{config: monitorV3Config()}
	now := time.Date(2026, 11, 8, 1, 24, 0, 0, time.UTC)
	through := now.Add(-time.Minute)
	r.through = &through
	r.config.MinRequests = 5
	r.slotFacts.Metrics = []ChannelMonitorV3Fact{{GroupID: 4, Model: "any-group-model", Slot: now.Truncate(10 * time.Minute), Success: 1}}
	s := NewChannelMonitorV3Service(r, nil)
	s.now = func() time.Time { return now }
	out, err := s.PlazaStatuses(context.Background(), []int64{4}, false)
	require.NoError(t, err)
	require.Equal(t, 180, out[4].WindowMinutes)
	require.Len(t, out[4].Cells, 18)
	require.Equal(t, 10*time.Minute, out[4].Cells[1].Start.Sub(out[4].Cells[0].Start))
	require.Equal(t, "observed", out[4].Cells[17].Status, "a completed successful request is not an empty slot")
	require.Equal(t, "requests", out[4].Cells[17].Source)
	require.Nil(t, out[4].RequestSuccessRate, "one request is not enough for a percentage")
	r.slotFacts.Metrics[0].Errors = 1
	out, err = s.PlazaStatuses(context.Background(), []int64{4}, false)
	require.NoError(t, err)
	require.Equal(t, "errors_observed", out[4].Cells[17].Status)
	require.Nil(t, out[4].RequestSuccessRate, "sparse failures do not justify a percentage")
	r.slotFacts.Metrics = nil
	out, err = s.PlazaStatuses(context.Background(), []int64{4}, false)
	require.NoError(t, err)
	require.Equal(t, "collecting", out[4].Cells[17].Status)
	require.Equal(t, "unknown", out[4].Cells[16].Status, "an empty historical slot stays unknown")
	require.Nil(t, out[4].RequestSuccessRate)
}

func TestChannelMonitorV3SupportsPublicDomesticGroupChecks(t *testing.T) {
	for _, platform := range []string{PlatformKimi, PlatformDeepseek, PlatformZhipu, PlatformMiniMax} {
		t.Run(platform, func(t *testing.T) {
			require.True(t, channelMonitorV3ProbeGroupSupported(&Group{Platform: platform, Status: StatusActive}))
		})
	}
	require.False(t, channelMonitorV3ProbeGroupSupported(&Group{Platform: PlatformOpenAI, IsExclusive: true}))
}

type monitorPublicGroupsFake struct{ monitorV3GroupsFake }

func (g monitorPublicGroupsFake) ListActive(context.Context) ([]Group, error) {
	out := []Group{}
	for _, group := range g.monitorV3GroupsFake {
		out = append(out, group)
	}
	return out, nil
}

func TestChannelMonitorV3StatusCatalogIncludesSelectedPublicGroupsOnly(t *testing.T) {
	groups := monitorPublicGroupsFake{monitorV3GroupsFake{
		1: {ID: 1, Name: "GPT", Platform: PlatformOpenAI, Status: StatusActive},
		2: {ID: 2, Name: "DeepSeek", Platform: PlatformDeepseek, Status: StatusActive},
		3: {ID: 3, Name: "private", Platform: PlatformKimi, Status: StatusActive, IsExclusive: true},
		4: {ID: 4, Name: "disabled", Platform: PlatformMiniMax, Status: "disabled"},
		5: {ID: 5, Name: "Kimi", Platform: PlatformKimi, Status: StatusActive},
	}}
	config := monitorV3Config()
	config.GroupProbe.Groups = []ChannelMonitorV3GroupProbeTarget{
		{GroupID: 1, Model: "gpt-mini", Enabled: true},
		{GroupID: 2, Model: "deepseek-chat", Enabled: false},
		{GroupID: 3, Model: "kimi", Enabled: true},
		{GroupID: 4, Model: "minimax", Enabled: true},
	}
	s := NewChannelMonitorV3Service(&monitorV3RepoFake{config: config}, groups)
	public, err := s.PublicStatusGroups(context.Background(), false)
	require.NoError(t, err)
	require.Len(t, public, 1, "unselected, paused legacy, private and inactive groups stay hidden")
	require.Equal(t, int64(1), public[0].ID)
	for _, group := range public {
		require.False(t, group.IsExclusive)
		require.NotEqual(t, "disabled", group.Name)
	}
	probe := ChannelMonitorV3GroupProbeConfig{Enabled: true, Groups: []ChannelMonitorV3GroupProbeTarget{
		{GroupID: 2, Model: "deepseek-chat", DisplayName: " DeepSeek 官方 ", Enabled: true},
		{GroupID: 5, DisplayName: "Kimi 公共", Enabled: false},
	}}
	require.NoError(t, s.validateGroupProbes(context.Background(), &probe))
	require.Equal(t, "DeepSeek 官方", probe.Groups[0].DisplayName)
	probe.Groups[0].GroupID = 3
	require.Error(t, s.validateGroupProbes(context.Background(), &probe), "private groups must not receive public status checks")
}

func TestChannelMonitorV3PublicationIsExplicitIndependentAndEmptySafe(t *testing.T) {
	listed, hidden := true, false
	config := monitorV3Config()
	config.PublicEnabled = true
	config.GroupProbe = ChannelMonitorV3GroupProbeConfig{Enabled: false, Groups: []ChannelMonitorV3GroupProbeTarget{
		{GroupID: 2, DisplayName: "Public DeepSeek", Listed: &listed},
		{GroupID: 5, Model: "retained-model", Listed: &hidden},
	}}
	repo := &monitorV3RepoFake{config: config}
	s := NewChannelMonitorV3Service(repo, monitorPublicGroupsFake{monitorV3GroupsFake{
		2: {ID: 2, Name: "DeepSeek", Platform: PlatformDeepseek, Status: StatusActive},
		5: {ID: 5, Name: "Kimi", Platform: PlatformKimi, Status: StatusActive},
	}})
	for _, anonymous := range []bool{true, false} {
		public, err := s.PublicStatusGroups(context.Background(), anonymous)
		require.NoError(t, err)
		require.Len(t, public, 1)
		require.Equal(t, int64(2), public[0].ID)
	}
	repo.config.GroupProbe.Groups[0].Listed = &hidden
	public, err := s.PublicStatusGroups(context.Background(), true)
	require.NoError(t, err)
	require.Empty(t, public, "an explicitly empty publication is valid")
	repo.config.PublicEnabled = false
	_, err = s.PublicStatusGroups(context.Background(), true)
	require.ErrorIs(t, err, ErrChannelMonitorV3NotFound, "empty selection must not bypass opt-in")
	_, err = s.PublicStatusGroups(context.Background(), false)
	require.NoError(t, err)
}

type groupProbeJSONConfigRepo struct{ *groupProbeRepoFake }

func (r groupProbeJSONConfigRepo) GetConfig(context.Context) (*ChannelMonitorV3Config, error) {
	raw, err := json.Marshal(r.config)
	if err != nil {
		return nil, err
	}
	var config ChannelMonitorV3Config
	err = json.Unmarshal(raw, &config)
	return &config, err
}

func TestChannelMonitorV3ListedProbeReauthorizesDecodedValuesAndHidesWithoutSpending(t *testing.T) {
	repo, prober, executor := groupProbeFixture()
	listed := true
	repo.config.GroupProbe.Groups[0].Listed = &listed
	prober.repo = groupProbeJSONConfigRepo{repo}
	now := time.Now().UTC()
	prober.RunDue(context.Background(), now)
	require.Equal(t, 1, executor.calls, "decoded boolean pointers differ but authorization values are equal")
	listed = false
	prober.RunDue(context.Background(), now.Add(ChannelMonitorV3ProbeInterval))
	require.Equal(t, 1, executor.calls, "hidden targets must never submit checks even if enabled is stale")
	s := NewChannelMonitorV3Service(repo, nil)
	require.Error(t, s.validateGroupProbes(context.Background(), &repo.config.GroupProbe))
}
