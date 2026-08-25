// ─── Shared Types ─────────────────────────────────────────────────────────────

export interface ProcessData {
  pid: number;
  name: string;
  cpu: number;
  memory: number;
}

export interface HistoryEntry {
  url: string;
  title: string;
  last_visit_time: number;
  browser: string;
}

export interface SystemInfo {
  hostname: string;
  os: string;
  architecture: string;
  num_cpus: number;
  platform: string;
  platform_version: string;
  kernel_version: string;
}

export interface CPUStat {
  usage_percent: number;
  core_count: number;
}

export interface RAMStat {
  total_gb: number;
  used_gb: number;
  free_gb: number;
  used_percent: number;
}

export interface DiskStat {
  mount: string;
  total_bytes?: number;
  used_bytes?: number;
  free_bytes?: number;
  total_gb: number;
  used_gb: number;
  free_gb: number;
  used_percent: number;
}

export interface NetStat {
  interface: string;
  bytes_sent: number;
  bytes_recv: number;
}

export interface BatteryStat {
  available: boolean;
  percent: number;
  plugged: boolean;
  charging: boolean;
  charge_rate_w: number;
  state: 'charging' | 'discharging' | 'full' | 'empty' | 'idle' | 'unknown';
}

export interface HardwareStats {
  cpu: CPUStat;
  ram: RAMStat;
  disks: DiskStat[];
  network: NetStat[];
  battery?: BatteryStat;
  uptime_human: string;
}

export interface ServiceEntry {
  name: string;
  status: 'running' | 'stopped' | 'unknown';
  pid?: string;
}

export interface PortEntry {
  protocol: string;
  local_addr: string;
  state?: string;
  pid?: number;
  process?: string;
}

export interface AppEntry {
  name: string;
  version?: string;
  bundle_id?: string;
  path?: string;
  source: string;
}

export interface USBDevice {
  name: string;
  vendor_id?: string;
  product_id?: string;
  manufacturer?: string;
  serial_number?: string;
  speed?: string;
}

export interface OSUpdateInfo {
  last_update_time?: string;
  last_update_raw?: string;
  pending_updates?: string[];
  pending_count: number;
  source: string;
}

export interface AppFocusSummary {
  app_name: string;
  total_focus_seconds: number;
  session_count: number;
}

export interface ActiveWindowData {
  current_app: string;
  app_summaries: AppFocusSummary[];
  cumulative_summaries: AppFocusSummary[];
}

export interface FocusCacheData {
  device_id: string;
  app_summaries: AppFocusSummary[];
}

export interface BrowserHistoryArchiveData {
  device_id: string;
  from: string;
  to: string;
  count: number;
  entries: HistoryEntry[];
}

export interface DeviceData {
  SystemInfo?: SystemInfo;
  ProcessMonitor?: { top_processes: ProcessData[] };
  BrowserHistory?: {
    top_recent_urls: HistoryEntry[];
    new_history_entries?: HistoryEntry[];
  };
  HardwareStats?: HardwareStats;
  Services?: { services: ServiceEntry[]; source?: string };
  NetworkPorts?: { open_ports: PortEntry[]; source?: string };
  InstalledApps?: { installed_apps: AppEntry[]; count: number; source?: string };
  USBEvents?: { usb_devices: USBDevice[]; count: number; source?: string };
  OSUpdates?: { os_updates: OSUpdateInfo };
  ActiveWindowTracker?: ActiveWindowData;
}

export interface Device {
  device_id: string;
  hostname?: string;
  timestamp?: string;
  last_seen?: string;
  data?: DeviceData;
}

export type UserRole = 'admin' | 'manager' | 'viewer';

export interface DashboardUser {
  id: string;
  email: string;
  name: string;
  role: UserRole;
  status?: string;
  created_at?: string;
  updated_at?: string;
}

export interface AgentRelease {
  version: string;
  os: 'darwin' | 'linux' | 'windows';
  arch: 'amd64' | 'arm64' | string;
  download_url: string;
  checksum_sha256: string;
  published_at?: string;
}

export interface AgentBuildArtifact {
  os: AgentRelease['os'];
  arch: string;
  file_name: string;
  s3_key: string;
  download_url: string;
  checksum_sha256: string;
  size_bytes: number;
}

export interface AgentBuildJob {
  id: string;
  version: string;
  api_url: string;
  platforms: AgentRelease['os'][];
  archs: string[];
  status: 'queued' | 'building' | 'uploading' | 'publishing' | 'published' | 'failed';
  error?: string;
  logs: string[];
  artifacts: AgentBuildArtifact[];
  created_at: string;
  updated_at: string;
  started_at?: string;
  finished_at?: string;
}

export interface EnterprisePolicy {
  sync_interval_seconds: number;
  telemetry_retention_days: number;
  delta_upload_enabled: boolean;
  cache_unchanged_collector_data: boolean;
  browser_history_mode: 'disabled' | 'domain_only' | 'full_url';
  browser_history_limit: number;
  collect_system_info: boolean;
  collect_hardware_stats: boolean;
  collect_processes: boolean;
  collect_browser_history: boolean;
  collect_active_window: boolean;
  collect_services: boolean;
  collect_network_ports: boolean;
  collect_installed_apps: boolean;
  collect_os_updates: boolean;
  collect_usb_devices: boolean;
}

export type DeviceTab =
  | 'overview'
  | 'hardware'
  | 'processes'
  | 'browser'
  | 'services'
  | 'ports'
  | 'apps'
  | 'security'
  | 'focus'
  | 'sysinfo';
