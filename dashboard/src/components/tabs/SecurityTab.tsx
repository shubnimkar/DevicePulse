'use client';

import { USBDevice, OSUpdateInfo } from '@/types';

interface Props {
  usb?:   { usb_devices: USBDevice[] | null; count: number; source?: string };
  osUpd?: { os_updates: OSUpdateInfo };
}

export default function SecurityTab({ usb, osUpd }: Props) {
  const upd        = osUpd?.os_updates;
  const usbDevices = usb?.usb_devices ?? [];

  return (
    <div className="security-grid">
      {/* OS Updates */}
      <div className="sec-card">
        <div className="sec-card-title">OS Updates</div>
        {upd ? (
          <>
            <div className="sec-row">
              <span className="sec-label">Source</span>
              <span className="sec-value mono">{upd.source}</span>
            </div>
            <div className="sec-row">
              <span className="sec-label">Last Updated</span>
              <span className="sec-value">
                {upd.last_update_time
                  ? new Date(upd.last_update_time).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })
                  : upd.last_update_raw ?? '—'}
              </span>
            </div>
            <div className="sec-row">
              <span className="sec-label">Pending</span>
              <span className={`sec-value ${upd.pending_count > 0 ? 'warn' : 'ok'}`}>
                {upd.pending_count} update{upd.pending_count !== 1 ? 's' : ''}
              </span>
            </div>
            {upd.pending_updates && upd.pending_updates.length > 0 && (
              <div className="pending-list">
                {upd.pending_updates.slice(0, 5).map((u, i) => (
                  <div key={i} className="pending-item">↑ {u}</div>
                ))}
                {upd.pending_updates.length > 5 && (
                  <div className="pending-item muted">+{upd.pending_updates.length - 5} more</div>
                )}
              </div>
            )}
          </>
        ) : (
          <div className="no-data" style={{ padding: '1rem 0' }}>No update data yet.</div>
        )}
      </div>

      {/* USB Devices */}
      <div className="sec-card">
        <div className="sec-card-title">USB Devices ({usb?.count ?? 0})</div>
        {usbDevices.length ? (
          <ul className="usb-list">
            {usbDevices.map((d, i) => (
              <li key={i} className="usb-item">
                <div className="usb-name">{d.name || 'Unknown Device'}</div>
                <div className="usb-meta">
                  {d.manufacturer && <span>{d.manufacturer}</span>}
                  {d.vendor_id && <span className="mono">{d.vendor_id}:{d.product_id}</span>}
                  {d.speed && <span className="usb-speed">{d.speed}</span>}
                </div>
              </li>
            ))}
          </ul>
        ) : (
          <div className="no-data" style={{ padding: '1rem 0' }}>No USB devices connected.</div>
        )}
      </div>
    </div>
  );
}
