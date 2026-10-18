package proxy

import (
	"time"
)

type ProviderRuntimeSnapshot struct {
	Name              string
	KeyCount          int
	AvailableKeyCount int
	BusyUntil         time.Time
	BusyBackoffStep   int
	BusyProbeInFlight int

	DeactivatedReason  string
	DeactivatedMessage string
	DeactivatedUntil   time.Time

	CircuitState  string
	CircuitOpenIn time.Duration
}

type ClientRuntimeSnapshot struct {
	Mode           string
	PinnedProvider string

	CurrentProvider          string
	CurrentProviders         map[string]string
	LastSwitch               *ProviderSwitchEvent
	LastRequest              *RequestOutcomeEvent
	StickyBindingCount       int
	ResponseLookupCount      int
	DynamicFeatureCacheCount int

	Providers []ProviderRuntimeSnapshot
}

type RouterRuntimeSnapshot struct {
	Clients map[ClientType]ClientRuntimeSnapshot
}

func (r *Router) RuntimeSnapshot() RouterRuntimeSnapshot {
	r.mu.RLock()
	proxies := make(map[ClientType]*ClientProxy, len(r.proxies))
	for ct, p := range r.proxies {
		proxies[ct] = p
	}
	r.mu.RUnlock()

	now := time.Now()
	out := RouterRuntimeSnapshot{
		Clients: make(map[ClientType]ClientRuntimeSnapshot, len(proxies)),
	}

	for ct, p := range proxies {
		if p == nil {
			continue
		}
		out.Clients[ct] = p.runtimeSnapshot(now)
	}

	return out
}

func (cp *ClientProxy) runtimeSnapshot(now time.Time) ClientRuntimeSnapshot {
	cp.mu.RLock()
	defer cp.mu.RUnlock()

	providers := make([]ProviderRuntimeSnapshot, 0, len(cp.providers))
	for i := range cp.providers {
		ps := ProviderRuntimeSnapshot{
			Name:     cp.providers[i].Name,
			KeyCount: len(cp.providerKeys[i]),
		}
		ps.AvailableKeyCount = cp.availableKeyCountLocked(i, now)
		if i < len(cp.providerBusy) {
			ps.BusyUntil = cp.providerBusy[i].Until
			ps.BusyBackoffStep = cp.providerBusy[i].BackoffStep
			ps.BusyProbeInFlight = cp.providerBusy[i].ProbeInFlight
		}
		if i < len(cp.deactivated) {
			d := cp.deactivated[i]
			if !d.until.IsZero() && now.Before(d.until) {
				ps.DeactivatedReason = d.reason
				ps.DeactivatedMessage = d.message
				ps.DeactivatedUntil = d.until
			}
		}
		if i < len(cp.breakers) && cp.breakers[i] != nil {
			st, wait := cp.breakers[i].snapshot(now)
			ps.CircuitState = string(st)
			ps.CircuitOpenIn = wait
		} else {
			ps.CircuitState = string(circuitClosed)
		}
		providers = append(providers, ps)
	}

	var lastSwitch *ProviderSwitchEvent
	if !cp.lastSwitch.At.IsZero() {
		ls := cp.lastSwitch
		lastSwitch = &ls
	}
	var lastRequest *RequestOutcomeEvent
	if !cp.lastRequest.At.IsZero() {
		lr := cp.lastRequest
		lastRequest = &lr
	}

	return ClientRuntimeSnapshot{
		Mode:             string(cp.mode),
		PinnedProvider:   cp.pinnedProvider,
		CurrentProviders: cp.currentProvidersSnapshotLocked(),
		CurrentProvider: func() string {
			if len(cp.providers) == 0 {
				return ""
			}
			idx := cp.currentIndex
			if idx < 0 || idx >= len(cp.providers) {
				idx = 0
			}
			return cp.providers[idx].Name
		}(),
		LastSwitch:               lastSwitch,
		LastRequest:              lastRequest,
		StickyBindingCount:       len(cp.stickyBindings),
		ResponseLookupCount:      len(cp.responseLookup),
		DynamicFeatureCacheCount: len(cp.dynamicFeatureBindings),
		Providers:                providers,
	}
}

func (cp *ClientProxy) currentProvidersSnapshotLocked() map[string]string {
	if cp == nil || len(cp.providers) == 0 {
		return nil
	}

	out := map[string]string{}
	if name := providerNameAtIndex(cp.providers, cp.currentIndex); name != "" {
		out["default"] = name
	}
	switch cp.clientType {
	case ClientOpenAI:
		if name := providerNameAtIndex(cp.providers, cp.responsesIndex); name != "" {
			out[string(CapabilityOpenAIResponses)] = name
		}
	case ClientGoogle:
		if name := providerNameAtIndex(cp.providers, cp.geminiStreamIndex); name != "" {
			out[string(CapabilityGeminiStreamGenerate)] = name
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
