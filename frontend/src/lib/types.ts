export type Status =
  | "queued"
  | "active"
  | "paused"
  | "completed"
  | "error"
  | "refresh"
  | "verifying";

export interface Download {
  id: string;
  url: string;
  filename: string;
  dir: string;
  total_size: number;
  downloaded: number;
  status: Status;
  segments: number;
  speed_limit: number;
  headers: Record<string, string> | null;
  referer_url: string;
  checksum: Checksum | null;
  error: string;
  etag: string;
  last_modified: string;
  created_at: string;
  completed_at: string | null;
  queue_order: number;
  // Runtime fields (from progress events)
  speed?: number;
  eta?: number;
}

export interface Checksum {
  algorithm: string;
  value: string;
}

export interface Segment {
  download_id: string;
  index: number;
  start_byte: number;
  end_byte: number;
  downloaded: number;
  done: boolean;
}

export interface ProbeResult {
  filename: string;
  total_size: number;
  accepts_ranges: boolean;
  etag: string;
  last_modified: string;
  final_url: string;
  content_type: string;
}

export interface ProgressUpdate {
  id: string;
  downloaded: number;
  total_size: number;
  speed: number;
  eta: number;
  status: Status;
}

export interface Config {
  download_dir: string;
  max_concurrent: number;
  default_segments: number;
  global_speed_limit: number;
  server_port: number;
  minimize_to_tray: boolean;
  max_retries: number;
  theme: string;
}

export interface Stats {
  active: number;
  queued: number;
  completed: number;
}

export interface AddRequest {
  url: string;
  filename: string;
  dir: string;
  segments: number;
  headers: Record<string, string>;
  referer_url: string;
  speed_limit: number;
  checksum: Checksum | null;
}
