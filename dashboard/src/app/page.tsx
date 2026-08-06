'use client';

import { useEffect, useState, useCallback } from 'react';
import { Device, AppFocusSummary, FocusCacheData, DeviceTab } from '@/types';
import { API, readHeaders, isOnline } from '@/lib/utils';
import DeviceCard from '@/components/DeviceCard';

export default function Home() {
  const [devices, setDevices]           = useState<Device[]>([]);
  const [loading, setLoading]           = useState(true);
  const [syncInterval, setSyncInterval] = useState(10);
  const [isUpdating, setIsUpdating]     = useState(false);
  const [activeTab, setActiveTab]       = useState<Record<string, DeviceTab>>({});
  const [focusCache, setFocusCache]     = useState<Record<string, AppFocusSummary[]>>({});

  // ── Fetching ─────────────────────────────────────────────────────────────────

  const fetchAll = useCallback(async () => {
    try {
      const devRes = await fetch(`${API}/devices`, { headers: readHeaders() });
      if (devRes.ok) {
        const data: Device[] = await devRes.json();
        const devList = data ?? [];
        setDevices(devList);

        const focusResults = await Promise.allSettled(
          devList.map(d =>
            fetch(`${API}/focus/${d.device_id}`, { headers: readHeaders() }).then(r =>
              r.ok ? (r.json() as Promise<FocusCacheData>) : null
            )
          )
        );
        const newFocusCache: Record<string, AppFocusSummary[]> = {};
        focusResults.forEach((result, i) => {
          if (result.status === 'fulfilled' && result.value) {
            newFocusCache[devList[i].device_id] = result.value.app_summaries ?? [];
          }
        });
        setFocusCache(newFocusCache);
      }
    } catch (e) {
      console.error('Fetch error:', e);
    } finally {
      setLoading(false);
    }
  }, []);

  const fetchPolicy = useCallback(async () => {
    try {
      const res = await fetch(`${API}/policy`, { headers: readHeaders() });
      if (res.ok) {
        const d = await res.json();
        if (d.sync_interval_seconds) setSyncInterval(d.sync_interval_seconds);
      }
    } catch {}
  }, []);

  useEffect(() => {
    fetchAll();
    fetchPolicy();
    const id = setInterval(fetchAll, 5000);
    return () => clearInterval(id);
  }, [fetchAll, fetchPolicy]);

  // ── Policy ────────────────────────────────────────────────────────────────────

  const handlePolicyUpdate = async (val: number) => {
    setSyncInterval(val);
    setIsUpdating(true);
    try {
      await fetch(`${API}/policy`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ sync_interval_seconds: val }),
      });
    } catch {}
    finally { setIsUpdating(false); }
  };

  const deleteDevice = async (deviceId: string) => {
    if (!confirm(`Remove device ${deviceId}?`)) return;
    try {
      await fetch(`${API}/devices/${deviceId}`, { method: 'DELETE', headers: readHeaders() });
      setDevices(d => d.filter(x => x.device_id !== deviceId));
    } catch {}
  };

  // ── Tabs ──────────────────────────────────────────────────────────────────────

  const getTab = (id: string): DeviceTab => activeTab[id] ?? 'overview';
  const setTab = (id: string, tab: DeviceTab) =>
    setActiveTab(prev => ({ ...prev, [id]: tab }));

  // ── Stats ─────────────────────────────────────────────────────────────────────

  const onlineCount  = devices.filter(d => isOnline(d.last_seen)).length;
  const offlineCount = devices.length - onlineCount;

  // ── Render ────────────────────────────────────────────────────────────────────

  return (
    <main className="dashboard-container">
      {/* ─── Top Header ─────────────────────────────────────────────────────── */}
      <header className="header">
        <div className="header-brand">
          <div className="header-logo">
            <span className="logo-pulse" />
            <span className="logo-inner" />
          </div>
          <div>
            <h1>DevicePulse</h1>
            <p>Enterprise Endpoint Telemetry</p>
          </div>
        </div>

        <div className="header-stats">
          <div className="hstat">
            <span className="hstat-num">{devices.length}</span>
            <span className="hstat-label">Devices</span>
          </div>
          <div className="hstat-divider" />
          <div className="hstat">
            <span className="hstat-num hstat-online">{onlineCount}</span>
            <span className="hstat-label">Online</span>
          </div>
          <div className="hstat-divider" />
          <div className="hstat">
            <span className="hstat-num hstat-offline">{offlineCount}</span>
            <span className="hstat-label">Offline</span>
          </div>
        </div>

        <div className="header-right">
          <div className="status-badge">
            <div className="status-dot" />
            {onlineCount} / {devices.length} Online
          </div>
          {isUpdating && (
            <span className="updating-label">Syncing…</span>
          )}
        </div>
      </header>

      {/* ─── Main Layout ────────────────────────────────────────────────────── */}
      <div className="dashboard-layout">
        {/* Sidebar */}
        <aside className="sidebar">
          {/* Policy Card */}
          <div className="policy-card glass-card">
            <div className="sidebar-section-title">
              <span>⚙️</span> Global Policy
            </div>
            <p className="policy-desc">Control agent sync frequency in real time.</p>
            <div className="slider-container">
              <div className="slider-header">
                <label>Sync Interval</label>
                <span className="slider-value">{syncInterval}s</span>
              </div>
              <input
                type="range"
                min="2"
                max="60"
                value={syncInterval}
                onChange={e => setSyncInterval(Number(e.target.value))}
                onMouseUp={e =>
                  handlePolicyUpdate(Number((e.target as HTMLInputElement).value))
                }
                disabled={isUpdating}
                className="policy-slider"
                aria-label="Sync interval in seconds"
              />
              <div className="slider-labels">
                <span>Fast (2s)</span>
                <span>Eco (60s)</span>
              </div>
            </div>
          </div>

          {/* Device List */}
        </aside>

        <section className="devices-section">
          {loading ? (
            <div className="loading" role="status" aria-label="Loading telemetry">
              Initializing Telemetry Stream…
            </div>
          ) : devices.length === 0 ? (
            <div className="empty-state">
              <div className="empty-icon">📡</div>
              <div className="empty-title">No devices connected</div>
              <div className="empty-sub">Start the agent to see live telemetry data.</div>
            </div>
          ) : (
            <div className="devices-grid">
              {devices.map((device, idx) => (
                <DeviceCard
                  key={device.device_id ?? idx}
                  device={device}
                  tab={getTab(device.device_id)}
                  onTabChange={tab => setTab(device.device_id, tab)}
                  onDelete={() => deleteDevice(device.device_id)}
                  cachedFocus={focusCache[device.device_id] ?? []}
                />
              ))}
            </div>
          )}
        </section>
      </div>
    </main>
  );
}
