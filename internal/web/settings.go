package web

import (
	"fmt"
	"maps"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// Settings returns the web interface's settings.
func (m *Manager) Settings() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.settings
}

// pagePolicy is the Content-Security-Policy of every response, computed
// when the settings load or change.
func (m *Manager) pagePolicy() string {
	if p := m.policy.Load(); p != nil {
		return *p
	}
	return contentSecurity
}

func (m *Manager) setPolicy(st Settings) {
	p := drawioPolicy(st)
	m.policy.Store(&p)
}

// SettingsPatch is a settings change; a nil field changes nothing.
// HiddenModels replaces the hidden model IDs of each provider it names, and
// only those; an empty list hides none of that provider's models.
// SubagentModels works the same way for the models a provider's subagents
// may use, in order, each one the provider lists now; an empty list lifts the
// limit. TitleModel
// sets the Utility model of each provider it names: a model ID, or
// store.WebTitleModelNone to opt out; an empty ID unsets it, so the provider
// uses its cheapest priced model again.
//
// CustomModels, when not nil, replaces every custom model; an empty list
// removes them all. TaskDefaults replaces the settings a new Task starts
// with; they are checked as a Task's selection is. Turning Terminal off
// closes every open terminal.
// UtilityLimit sets the daily limit of Utility calls, 0 or more; pointing at
// nil puts the default back. UtilityUnlimited lifts the limit without
// forgetting it.
// CompactionThreshold works the same way, store.MinCompactionThreshold to
// store.MaxCompactionThreshold; the default itself is stored as nil.
// GitHubMCP reaches open Tasks too (agentapi.GitHubMCPUser).
type SettingsPatch struct {
	TokenPrices       *map[string]map[string]store.WebTokenPrice
	SendDefault       *string
	DefaultProjectDir *string
	Terminal          *bool
	HiddenModels      map[string][]string
	SubagentModels    map[string][]string
	TitleModel        map[string]string
	CustomModels      *[]store.WebCustomModel
	TaskDefaults      *TaskDefaults
	UtilityLimit      **int
	UtilityUnlimited  *bool
	// SuggestReplies turns suggested replies on or off.
	SuggestReplies        *bool
	CompactionThreshold   **int
	GitHubMCP             *bool
	ThreadMessages        *bool
	GitLabAutoInvestigate *bool
	// GitLabMRPrompt sets the merge request description prompt; "" or the
	// default itself puts the default back.
	GitLabMRPrompt *string
	// CommitPrompt sets the commit message instructions the same way.
	CommitPrompt *string
	// OutputStyles replaces the output style library; a default no longer
	// in it is cleared.
	OutputStyles *[]store.WebOutputStyle
	// OutputStyle sets the default style by name; "" keeps the provider's
	// own.
	OutputStyle *string
	// CoAuthor sets the co-author trailer; a name must fit on one line.
	CoAuthor *store.WebCoAuthor
	// WorktreesDir sets the folder new worktrees go in; "" puts the
	// default back. A leading ~ is the home folder.
	WorktreesDir *string
	// DrawIO sets the draw.io editor's origin and whether it is enabled.
	// The HTTP handler checks the origin before it enables it.
	DrawIO *DrawIOSettings
}

// UpdateSettings applies p. An invalid value is refused with 400 and changes
// nothing. A change is stored and sent as a settings frame; no change writes
// nothing. Hidden models are a display preference: nothing else checks them.
// A Utility model must be one the provider lists now, and the provider must
// have the titles capability; neither is needed to unset it or opt out.
func (m *Manager) UpdateSettings(p SettingsPatch) (Settings, error) {
	var projectDir string
	if p.DefaultProjectDir != nil && *p.DefaultProjectDir != "" {
		if err := checkPathText("default_project_dir", *p.DefaultProjectDir); err != nil {
			return Settings{}, err
		}
		projectDir = filepath.Clean(*p.DefaultProjectDir)
		if err := checkDir("default_project_dir", projectDir); err != nil {
			return Settings{}, err
		}
	}
	var worktreesDir string
	if p.WorktreesDir != nil && *p.WorktreesDir != "" {
		var err error
		if worktreesDir, err = m.checkWorktreesDir(*p.WorktreesDir); err != nil {
			return Settings{}, err
		}
	}
	if p.TokenPrices != nil {
		count := 0
		for provider, models := range *p.TokenPrices {
			if !store.ValidHiddenModel(provider) {
				return Settings{}, newError(http.StatusBadRequest, "invalid pricing provider")
			}
			for model, rates := range models {
				count++
				if !store.ValidTokenPriceModel(model) || !rates.Valid() {
					return Settings{}, newError(http.StatusBadRequest, "token prices require a model ID and non-negative input/output rates; cache rates are optional")
				}
			}
		}
		if count > 1000 || len(*p.TokenPrices) > 100 {
			return Settings{}, newError(http.StatusBadRequest, "too many token prices")
		}
	}
	if p.SendDefault != nil && *p.SendDefault != store.WebSendSteer && *p.SendDefault != store.WebSendQueue {
		return Settings{}, newError(http.StatusBadRequest, "send_default must be %q or %q", store.WebSendSteer, store.WebSendQueue)
	}
	hidden := make(map[string][]string, len(p.HiddenModels))
	for provider, ids := range p.HiddenModels {
		if m.providers[provider] == nil {
			return Settings{}, newError(http.StatusBadRequest, msgUnknownProvider, clipRunes(displaytext.Sanitize(provider), maxDetailRunes))
		}
		if slices.ContainsFunc(ids, func(id string) bool { return !store.ValidHiddenModel(id) }) {
			return Settings{}, newError(http.StatusBadRequest, "a hidden model ID must be 1 to %d bytes without control characters", store.MaxHiddenModelBytes)
		}
		ids = slices.Clone(ids)
		slices.Sort(ids)
		if ids = slices.Compact(ids); len(ids) > store.MaxHiddenModels {
			return Settings{}, newError(http.StatusBadRequest, "at most %d models of a provider can be hidden", store.MaxHiddenModels)
		}
		hidden[provider] = ids
	}
	subagents := make(map[string][]string, len(p.SubagentModels))
	for provider, ids := range p.SubagentModels {
		if m.providers[provider] == nil {
			return Settings{}, newError(http.StatusBadRequest, msgUnknownProvider, clipRunes(displaytext.Sanitize(provider), maxDetailRunes))
		}
		if _, ok := m.providers[provider].(agentapi.SubagentModelUser); !ok {
			return Settings{}, newError(http.StatusBadRequest, "%s cannot limit subagent models", m.providers[provider].DisplayName())
		}
		if slices.ContainsFunc(ids, func(id string) bool { return !store.ValidHiddenModel(id) }) {
			return Settings{}, newError(http.StatusBadRequest, "a subagent model ID must be 1 to %d bytes without control characters", store.MaxHiddenModelBytes)
		}
		if ids = store.UniqueModelIDs(ids); len(ids) > store.MaxHiddenModels {
			return Settings{}, newError(http.StatusBadRequest, "at most %d subagent models of a provider can be allowed", store.MaxHiddenModels)
		}
		subagents[provider] = ids
	}
	for provider := range p.TitleModel {
		if m.providers[provider] == nil {
			return Settings{}, newError(http.StatusBadRequest, msgUnknownProvider, clipRunes(displaytext.Sanitize(provider), maxDetailRunes))
		}
	}
	if l := p.UtilityLimit; l != nil && *l != nil && **l < 0 {
		return Settings{}, newError(http.StatusBadRequest, "utility_daily_limit must be 0 or more")
	}
	if t := p.CompactionThreshold; t != nil && *t != nil && (**t < store.MinCompactionThreshold || **t > store.MaxCompactionThreshold) {
		return Settings{}, newError(http.StatusBadRequest, "compact_threshold must be %d to %d", store.MinCompactionThreshold, store.MaxCompactionThreshold)
	}
	var mrPrompt string
	if p.GitLabMRPrompt != nil {
		if mrPrompt = strings.TrimSpace(*p.GitLabMRPrompt); utf8.RuneCountInString(mrPrompt) > maxMRPromptRunes {
			return Settings{}, newError(http.StatusBadRequest, "gitlab_mr_prompt must be at most %d characters", maxMRPromptRunes)
		}
		if mrPrompt == defaultMRPrompt {
			mrPrompt = ""
		}
	}
	var commitPrompt string
	if p.CommitPrompt != nil {
		if commitPrompt = strings.TrimSpace(*p.CommitPrompt); utf8.RuneCountInString(commitPrompt) > maxMRPromptRunes {
			return Settings{}, newError(http.StatusBadRequest, "commit_prompt must be at most %d characters", maxMRPromptRunes)
		}
		if commitPrompt == defaultCommitPrompt {
			commitPrompt = ""
		}
	}
	var outputStyles []store.WebOutputStyle
	if p.OutputStyles != nil {
		var err error
		if outputStyles, err = cleanOutputStyles(*p.OutputStyles); err != nil {
			return Settings{}, err
		}
	}
	var coauthor store.WebCoAuthor
	if p.CoAuthor != nil {
		coauthor = store.WebCoAuthor{Enabled: p.CoAuthor.Enabled, Name: strings.TrimSpace(p.CoAuthor.Name)}
		if utf8.RuneCountInString(coauthor.Name) > maxCoAuthorRunes || strings.ContainsFunc(coauthor.Name, unicode.IsControl) {
			return Settings{}, newError(http.StatusBadRequest, "coauthor name must be one line of at most %d characters", maxCoAuthorRunes)
		}
	}
	var defaults TaskDefaults
	if p.TaskDefaults != nil {
		var err error
		if defaults, err = m.taskDefaults(*p.TaskDefaults); err != nil {
			return Settings{}, err
		}
	}
	m.settingsMu.Lock()
	defer m.settingsMu.Unlock()
	customKeysChanged := false
	if p.CustomModels != nil {
		cfg, err := m.store.Load()
		if err != nil {
			return Settings{}, fmt.Errorf("load custom model settings: %w", err)
		}
		list := slices.Clone(*p.CustomModels)
		for i := range list {
			c := &list[i]
			if c.APIKeyEnv == "" && c.APIKey == "" {
				c.APIKey = savedCustomKey(cfg.WebSettings.CustomModels, c.Name, c.BaseURL, c.WireAPI)
			}
		}
		if err := store.ValidCustomModels(list); err != nil {
			return Settings{}, newError(http.StatusBadRequest, "%s", err.Error())
		}
		customKeysChanged = !slices.Equal(list, cfg.WebSettings.CustomModels)
		p.CustomModels = &list
	}
	m.mu.Lock()
	current := m.settings
	for provider, ids := range hidden {
		info := m.infos[provider]
		if listed := info.Models; len(listed) > 0 && !slices.ContainsFunc(listed, func(mo agentapi.Model) bool { _, found := slices.BinarySearch(ids, mo.ID); return !found }) {
			m.mu.Unlock()
			return Settings{}, newError(http.StatusBadRequest, "at least one %s model must stay visible", info.DisplayName)
		}
	}
	for provider, ids := range subagents {
		info := m.infos[provider]
		for _, id := range ids {
			if !slices.ContainsFunc(info.Models, func(mo agentapi.Model) bool { return mo.ID == id }) {
				m.mu.Unlock()
				return Settings{}, newError(http.StatusBadRequest, "%s does not offer model %q", info.DisplayName, clipRunes(displaytext.Sanitize(id), maxDetailRunes))
			}
		}
	}
	for provider, model := range p.TitleModel {
		info := m.infos[provider]
		switch {
		case model == "", model == store.WebTitleModelNone:
		case m.titlerLocked(provider) == nil:
			m.mu.Unlock()
			return Settings{}, newError(http.StatusBadRequest, "%s cannot title threads with a model", m.providers[provider].DisplayName())
		case !slices.ContainsFunc(info.Models, func(mo agentapi.Model) bool { return mo.ID == model }):
			m.mu.Unlock()
			return Settings{}, newError(http.StatusBadRequest, "%s does not offer model %q", info.DisplayName, clipRunes(displaytext.Sanitize(model), maxDetailRunes))
		}
	}
	m.mu.Unlock()
	titles := p.TitleModel
	if p.CustomModels != nil {
		titles = withoutRemovedCustom(current, *p.CustomModels, hidden, subagents, titles)
		// Removing the last allowed models must not lift the limit unasked.
		for provider, ids := range subagents {
			if _, asked := p.SubagentModels[provider]; !asked && len(ids) == 0 {
				return Settings{}, newError(http.StatusBadRequest, "removing these custom models leaves %s subagents no allowed model; change Subagent models first", m.providers[provider].DisplayName())
			}
		}
	}
	next := current
	if p.TokenPrices != nil {
		next.TokenPrices = *p.TokenPrices
	}
	if p.SendDefault != nil {
		next.SendDefault = *p.SendDefault
	}
	if p.Terminal != nil {
		next.Terminal = *p.Terminal
	}
	if p.DefaultProjectDir != nil {
		next.DefaultProjectDir = projectDir
	}
	if p.WorktreesDir != nil {
		next.WorktreesDir = worktreesDir
	}
	if p.GitLabAutoInvestigate != nil {
		next.GitLabAutoInvestigate = *p.GitLabAutoInvestigate
	}
	if p.GitLabMRPrompt != nil {
		next.GitLabMRPrompt = mrPrompt
	}
	if p.CommitPrompt != nil {
		next.CommitPrompt = commitPrompt
	}
	if p.OutputStyles != nil {
		next.OutputStyles = outputStyles
		if !hasOutputStyle(outputStyles, next.OutputStyle) {
			next.OutputStyle = ""
		}
	}
	if p.OutputStyle != nil {
		name := strings.TrimSpace(*p.OutputStyle)
		if name != "" && !hasOutputStyle(next.OutputStyles, name) {
			return Settings{}, newError(http.StatusBadRequest, msgUnknownOutputName, name)
		}
		next.OutputStyle = name
	}
	if p.CoAuthor != nil {
		next.CoAuthor = coauthor
	}
	if p.GitHubMCP != nil {
		next.GitHubMCP = *p.GitHubMCP
	}
	if p.ThreadMessages != nil {
		next.ThreadMessages = *p.ThreadMessages
	}
	if p.DrawIO != nil {
		next.DrawIO = DrawIOSettings{}
		if p.DrawIO.Origin != "" || p.DrawIO.Enabled {
			origin, err := drawioOrigin(p.DrawIO.Origin)
			if err != nil {
				return Settings{}, newError(http.StatusBadRequest, "%s", err)
			}
			next.DrawIO = DrawIOSettings{Origin: origin, Enabled: p.DrawIO.Enabled}
		}
	}
	if len(hidden) > 0 {
		next.HiddenModels = withProviders(current.HiddenModels, hidden)
	}
	if len(subagents) > 0 {
		next.SubagentModels = withProviders(current.SubagentModels, subagents)
	}
	if len(titles) > 0 {
		next.TitleModel = withProviders(current.TitleModel, titles)
	}
	if p.CustomModels != nil {
		next.CustomModels = customModelsView(*p.CustomModels)
	}
	if p.TaskDefaults != nil {
		next.TaskDefaults = defaults
	}
	if p.SuggestReplies != nil {
		next.SuggestReplies = suggestSetting(*p.SuggestReplies)
	}
	if p.UtilityUnlimited != nil {
		next.UtilityUnlimited = *p.UtilityUnlimited
	}
	if p.UtilityLimit != nil {
		next.UtilityDailyLimit = *p.UtilityLimit
	}
	if t := p.CompactionThreshold; t != nil {
		next.CompactionThreshold = *t
		if *t != nil && **t == store.DefaultCompactionThreshold {
			next.CompactionThreshold = nil
		}
	}
	customChanged := customKeysChanged || !slices.Equal(next.CustomModels, current.CustomModels)
	limitChanged := (next.UtilityDailyLimit == nil) != (current.UtilityDailyLimit == nil) || next.UtilityDailyLimit != nil && *next.UtilityDailyLimit != *current.UtilityDailyLimit
	thresholdChanged := next.compactionThreshold() != current.compactionThreshold()
	subagentsChanged := !maps.EqualFunc(next.SubagentModels, current.SubagentModels, slices.Equal)
	if p.TokenPrices == nil && next.SendDefault == current.SendDefault && next.DefaultProjectDir == current.DefaultProjectDir && next.WorktreesDir == current.WorktreesDir && next.Terminal == current.Terminal && next.GitHubMCP == current.GitHubMCP && next.ThreadMessages == current.ThreadMessages && next.GitLabAutoInvestigate == current.GitLabAutoInvestigate && next.GitLabMRPrompt == current.GitLabMRPrompt && next.CommitPrompt == current.CommitPrompt && next.OutputStyle == current.OutputStyle && slices.Equal(next.OutputStyles, current.OutputStyles) && next.CoAuthor == current.CoAuthor && next.DrawIO == current.DrawIO && maps.EqualFunc(next.HiddenModels, current.HiddenModels, slices.Equal) && !subagentsChanged && maps.Equal(next.TitleModel, current.TitleModel) && !customChanged && next.TaskDefaults == current.TaskDefaults && !limitChanged && next.UtilityUnlimited == current.UtilityUnlimited && next.suggestReplies() == current.suggestReplies() && !thresholdChanged {
		return current, nil
	}
	var oldRoots []string
	if err := m.store.Update(func(cfg *store.Config) error {
		if p.TokenPrices != nil {
			cfg.WebSettings.TokenPrices = next.TokenPrices
		}
		cfg.WebSettings.SendDefault = next.SendDefault
		cfg.WebSettings.DefaultProjectDir = next.DefaultProjectDir
		cfg.WebSettings.WorktreesDir = next.WorktreesDir
		oldRoots = keptWorktreeRoots(cfg.WebSettings.WorktreeRoots, current.WorktreesDir, next.WorktreesDir)
		cfg.WebSettings.WorktreeRoots = oldRoots
		cfg.WebSettings.Terminal = next.Terminal
		cfg.WebSettings.GitHubMCP = next.GitHubMCP
		cfg.WebSettings.ThreadMessages = next.ThreadMessages
		cfg.WebSettings.GitLabAutoInvestigate = next.GitLabAutoInvestigate
		cfg.WebSettings.GitLabMRPrompt = next.GitLabMRPrompt
		cfg.WebSettings.CommitPrompt = next.CommitPrompt
		cfg.WebSettings.OutputStyles = next.OutputStyles
		cfg.WebSettings.OutputStyle = next.OutputStyle
		cfg.WebSettings.CoAuthor = next.CoAuthor
		cfg.WebSettings.DrawIO = store.WebDrawIO(next.DrawIO)
		cfg.WebSettings.TaskDefaults = store.WebTaskDefaults(next.TaskDefaults)
		cfg.WebSettings.UtilityDailyLimit = next.UtilityDailyLimit
		cfg.WebSettings.UtilityUnlimited = next.UtilityUnlimited
		cfg.WebSettings.SuggestReplies = next.SuggestReplies
		cfg.WebSettings.CompactionThreshold = next.CompactionThreshold
		cfg.WebSettings.HiddenModels = withProviders(cfg.WebSettings.HiddenModels, hidden)
		cfg.WebSettings.SubagentModels = withProviders(cfg.WebSettings.SubagentModels, subagents)
		cfg.WebSettings.TitleModel = withProviders(cfg.WebSettings.TitleModel, titles)
		if p.CustomModels != nil {
			cfg.WebSettings.CustomModels = slices.Clone(*p.CustomModels)
		}
		return nil
	}); err != nil {
		return Settings{}, fmt.Errorf("save web settings: %w", err)
	}
	if customChanged {
		m.setCustomModels(*p.CustomModels)
		m.reloadCustomCatalogs()
	}
	if subagentsChanged {
		m.setSubagentModels(next.SubagentModels)
	}
	if next.GitHubMCP != current.GitHubMCP {
		m.setGitHubMCP(next.GitHubMCP)
	}
	m.setGitRules(next)
	m.setPolicy(next)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings = next
	m.oldWorktreeRoots = oldRoots
	if !next.Terminal {
		m.closeTerminalsLocked(errTerminalOff)
	}
	m.broadcastLocked("settings", "", func(seq uint64) any { return settingsEvent{Seq: seq, Settings: next} })
	return next, nil
}
