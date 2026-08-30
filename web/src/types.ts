export type Classification = {
  document_type: string;
  sender: string;
  recipient: string;
  recipient_type: string;
  document_date: string;
  summary: string;
  suggested_folder: string;
  suggested_filename: string;
  physical_original_action: string;
  confidence: number;
  reasons: string[];
  sensitive: boolean;
  source: string;
  folder_rankings: { folder: string; confidence: number; reason: string }[];
};

export type Job = {
  id: string;
  source_filename: string;
  scan_timestamp: string;
  updated_at: string;
  status: string;
  page_count: number;
  input_kind: "scan" | "digital_pdf" | "mixed_pdf";
  text_source: "ocr" | "embedded";
  summary: string;
  confidence: number;
  physical_original_action: string;
  error: string;
  final_path: string;
  classification: Classification;
  suggested_path: string;
  urls: {
    current: string;
    raw: string;
    text: string;
    pages: string;
  };
};

export type Dashboard = {
  settings: {
    inbox: string;
    archive_root: string;
    archive_exists: boolean;
    scanner_share_checked: boolean;
    scanner_share_ready: boolean;
    model: string;
  };
  stats: {
    review: number;
    archived: number;
    failed: number;
    total: number;
  };
  folders: string[];
  review_jobs: Job[];
  recent_jobs: Job[];
  all_jobs: Job[];
};

export type ProgressEvent = {
  at: string;
  level: string;
  phase: string;
  step: string;
  message: string;
  current?: number;
  total?: number;
  percent: number;
  done?: boolean;
};

export type OCRPage = {
  page: number;
  width: number;
  height: number;
  image_url: string;
  boxes: {
    left: number;
    top: number;
    width: number;
    height: number;
    confidence: number;
    text: string;
  }[];
};
