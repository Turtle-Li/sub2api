package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 重构前各处手写的平台列表，作为平台清单派生结果的等价基准；重构后新登记的平台
// （Command Code、Cline、WorkBuddy）按同类平台（OpenCode）的位置补入。
var (
	legacyDisplayOrder = []string{
		PlatformAnthropic, PlatformOpenAI, PlatformGemini, PlatformAntigravity, PlatformGrok,
		PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax, PlatformOpenCodeGo, PlatformTypeSafe,
		PlatformCommandCode,
		PlatformCline,
		PlatformWorkbuddy,
	}
	legacyCompositePrecedence = []string{
		PlatformAnthropic, PlatformGemini, PlatformOpenAI, PlatformAntigravity, PlatformGrok,
		PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax, PlatformOpenCodeGo, PlatformTypeSafe,
		PlatformCommandCode,
		PlatformCline,
		PlatformWorkbuddy,
	}
	legacyOpenAIGateway = []string{
		PlatformOpenAI, PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax, PlatformOpenCodeGo,
		PlatformCommandCode,
		PlatformCline,
		PlatformWorkbuddy,
	}
	legacyCNProviders    = []string{PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax}
	legacyLiteLLMByPlatf = map[string]string{
		PlatformAnthropic: "anthropic", PlatformOpenAI: "openai", PlatformGemini: "gemini",
		PlatformAntigravity: "anthropic", PlatformGrok: "xai", PlatformKimi: "moonshot",
		PlatformZhipu: "zhipu", PlatformDeepseek: "deepseek", PlatformMiniMax: "minimax",
		PlatformOpenCodeGo: "opencode-go", PlatformTypeSafe: "typesafe", PlatformCommandCode: "", PlatformCline: "", PlatformWorkbuddy: "",
	}
)

func TestPlatformListMatchesLegacyLists(t *testing.T) {
	require.Equal(t, legacyDisplayOrder, ConcretePlatformIDs())
	require.Equal(t, legacyCompositePrecedence, CompositePrecedencePlatformIDs())
	require.Equal(t, legacyOpenAIGateway, PlatformIDsWhere(func(spec PlatformSpec) bool {
		return spec.Gateway == PlatformGatewayOpenAI
	}))
	require.Equal(t, legacyCNProviders, PlatformIDsWhere(func(spec PlatformSpec) bool { return spec.CNProvider }))
	for platform, provider := range legacyLiteLLMByPlatf {
		spec, ok := LookupPlatform(platform)
		require.True(t, ok, platform)
		require.Equal(t, provider, spec.LiteLLMProvider, platform)
	}
}

func TestPlatformListPredicates(t *testing.T) {
	for _, platform := range legacyDisplayOrder {
		require.True(t, IsConcretePlatform(platform), platform)
		require.True(t, IsGroupPlatform(platform), platform)
		spec, ok := LookupPlatform(platform)
		require.True(t, ok, platform)
		require.NotEmpty(t, spec.DisplayName, platform)
		require.NotEmpty(t, spec.Gateway, platform)
	}
	require.False(t, IsConcretePlatform(PlatformComposite))
	require.True(t, IsGroupPlatform(PlatformComposite))
	for _, invalid := range []string{"", "moonshot", "Kimi", "openai ", "glm", "bogus"} {
		require.False(t, IsConcretePlatform(invalid), invalid)
		require.False(t, IsGroupPlatform(invalid), invalid)
		require.False(t, UsesOpenAIGateway(invalid), invalid)
	}
}

func TestPlatformsReturnsCopy(t *testing.T) {
	platforms := Platforms()
	platforms[0].ID = "mutated"
	require.Equal(t, PlatformAnthropic, Platforms()[0].ID)
}

// WorkBuddy 与 OpenCode / Command Code / Cline 同构：它是账号渠道而不是模型厂商，
// 因此可被任一模型厂商分组添加，分组候选池展开必须包含它；composite 与 Antigravity
// 的既有语义不变。
func TestWorkbuddyBindsToModelProviderGroups(t *testing.T) {
	require.True(t, IsMultiModelPlatform(PlatformWorkbuddy))
	require.False(t, IsModelProviderPlatform(PlatformWorkbuddy))
	require.True(t, UsesOpenAIGateway(PlatformWorkbuddy))
	require.True(t, IsGroupPlatform(PlatformWorkbuddy))
	require.True(t, IsConcretePlatform(PlatformWorkbuddy))

	for _, family := range []string{
		PlatformOpenAI, PlatformGrok, PlatformAnthropic, PlatformKimi,
		PlatformZhipu, PlatformDeepseek, PlatformMiniMax,
	} {
		require.True(t, IsModelProviderPlatform(family), family)
		require.True(t, IsGroupAccountPlatformCompatible(family, PlatformWorkbuddy), family)
		require.Contains(t, GroupAccountPlatforms(family), PlatformWorkbuddy, family)
	}

	// 反向不成立：workbuddy 不是模型厂商，其它聚合平台账号不能进 workbuddy 分组；
	// 同平台绑定与 composite 绑定保持既有语义。
	require.False(t, IsGroupAccountPlatformCompatible(PlatformWorkbuddy, PlatformKimi))
	require.True(t, IsGroupAccountPlatformCompatible(PlatformWorkbuddy, PlatformWorkbuddy))
	require.True(t, IsGroupAccountPlatformCompatible(PlatformComposite, PlatformWorkbuddy))
	// Gemini 原生（antigravity）不接受多模型聚合账号。
	require.False(t, IsGroupAccountPlatformCompatible(PlatformAntigravity, PlatformWorkbuddy))
}
