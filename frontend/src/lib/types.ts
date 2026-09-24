export type Device = {
  assigned_id: number;
  hostname: string;
  username?: string;
  os_name?: string;
  os_pretty_name?: string;
  os_version?: string;
  kernel_release?: string;
  kernel_arch?: string;
  cpu_model?: string;
  cpu_physical_cores?: number;
  cpu_logical_cores?: number;
  gpu_info?: string;
  memory_total?: number;
  memory_used?: number;
  disk_info?: string;
  local_ip?: string;
  connected: boolean;
  last_seen?: string;
  first_seen?: string;
  checkin_status: number;
  student_name?: string;
  student_num?: string;
  checkin_time?: string;
  checkout_time?: string;
  cpu_pct?: number;
  mem_pct?: number;
  disk_pct?: number;
  temp_c?: number;
  load1?: number;
  client_version?: string;
  mac_address?: string;
  shell?: string;
  de_name?: string;
  uptime?: number;
};

export type CommandLog = {
  id: number;
  parent_id?: number;
  target_type: string;
  target_id?: number;
  command: string;
  status: string;
  output: string;
  error_output: string;
  created_at: string;
  duration_ms: number;
  children?: CommandLog[];
};

export type Deployment = {
  mode: string;
  node_id: string;
  room_name: string;
  cloud_url: string;
  token_set: boolean;
  allow_insecure: boolean;
  device_id_start: number;
  advertise_ip: string;
};

export type Room = {
  id: string;
  name: string;
  online: boolean;
  devices: Device[];
  last_seen: string;
  transfer_phase?: string;
  total_commands: number;
  recent_commands?: CommandLog[];
  distribution?: DistributeTask | null;
};

export type Preset = { name: string; desc: string; command: string; color: string };
export type NetRule = { type: string; value: string };

export type Settings = {
  hostname_prefix: string;
  presets: Preset[];
  checkin_config: {
    welcome_text: string;
    warning_text: string;
    post_checkin_msg: string;
    post_checkout_cmd: string;
    post_checkout_msg: string;
  };
  screen_monitor_enabled: boolean;
  maintenance: {
    cmd_retention_days: number;
    cmd_retention_max: number;
    event_retention_day: number;
    disk_alert_pct: number;
    temp_alert_c: number;
    mem_alert_pct: number;
  };
  deployment?: Deployment;
};

export type DistributeTask = {
  status: string;
  suggested_ip?: string;
  files?: string[];
  save_dir?: string;
  server_ip?: string;
  post_cmd?: string;
  active_file?: string;
  active_idx?: number;
  progresses?: Record<string, { device_id: number; hostname?: string; percentage: number; status: string; error?: string; speed_mbps?: number }>;
};

export type DistFile = { name: string; size: number; mod_time: string };

export type BroadcastItem = {
  id: number;
  page_id: number;
  item_type: string;
  content: string;
  pos_x: number;
  pos_y: number;
  width: number;
  height: number;
  font_size: string;
  font_color: string;
  font_weight: string;
  text_align: string;
  bg_color: string;
  border_radius: string;
  animation: string;
  z_index: number;
  extra_json: string;
};

export type BroadcastPage = {
  id: number;
  mode: string;
  title: string;
  sort_order: number;
  duration_ms: number;
  bg_color: string;
  transition: string;
  items?: BroadcastItem[];
};

export type BroadcastFont = { id: number; name: string; filename: string; format: string; original_name: string };

export type BroadcastConfig = {
  active_font: string;
  countdown_target: string;
  base_url: string;
  reference_width: string;
  pushed_state: string;
};

export type AdminEvent = { event: string; data?: Record<string, unknown> };
