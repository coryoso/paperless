export type UploadCandidate = {
  name: string;
  type: string;
};

const supportedMimeTypes = new Set(["application/pdf", "image/png", "image/jpeg"]);
const supportedExtensions = [".pdf", ".png", ".jpg", ".jpeg"];

export function isSupportedDocument(file: UploadCandidate): boolean {
  if (supportedMimeTypes.has(file.type.toLowerCase())) {
    return true;
  }

  const name = file.name.toLowerCase();
  return supportedExtensions.some((extension) => name.endsWith(extension));
}

export function pickSupportedDocument<T extends UploadCandidate>(files: ArrayLike<T>): T | null {
  for (let index = 0; index < files.length; index += 1) {
    const file = files[index];
    if (file && isSupportedDocument(file)) {
      return file;
    }
  }

  return null;
}

export function formatFileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}
