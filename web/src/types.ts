export type Classification = {
  document_type: string;
  sender: string;
  recipient: string;
  detected_recipient?: string;
  recipient_profile_id?: number;
  recipient_type: string;
  recipient_scope?: string;
  recipient_evidence?: string;
  recipient_needs_review?: boolean;
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
  paper_recommendations?: Record<string, string>;
  database_backup?: { directory: string; latest: string; count: number; available: boolean };
  recipient_profiles?: RecipientProfile[];
  learning_count?: number;
  learning_path?: string;
  settings: {
    inbox: string;
    archive_root: string;
    archive_exists: boolean;
    archive_error: string;
    setup_required: boolean;
    setup_step: "documents" | "scanner" | "model" | "ready" | "complete";
    scanner_share_checked: boolean;
    scanner_share_ready: boolean;
    model: string;
    model_provider: "ollama" | "fm" | "bonsai";
    model_enabled: boolean;
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

export type RecipientProfile = { id: number; name: string; scope: string; aliases: string[]; folder_prefix: string };

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

export type TextBlock = { kind: "heading" | "paragraph" | "pre" | "table" | "columns"; text?: string; rows?: string[][]; columns?: TextBlock[][] };
export type TextLayout = { pages: { page: number; blocks: TextBlock[] }[]; text: string; markdown: string };
