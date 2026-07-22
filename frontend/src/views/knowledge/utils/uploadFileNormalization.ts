/**
 * Windows may expose an .mhtml file through its 8.3 short name as .MHT.
 * Normalize only the multipart filename so existing .mhtml parser rules work;
 * the underlying Blob data is reused unchanged.
 */
export function normalizeUploadFile(file: File): File {
  if (!/\.mht$/i.test(file.name)) return file

  const normalized = new File(
    [file],
    file.name.replace(/\.mht$/i, '.mhtml'),
    { type: file.type, lastModified: file.lastModified },
  )
  const relativePath = (file as File & { webkitRelativePath?: string }).webkitRelativePath
  if (relativePath) {
    Object.defineProperty(normalized, 'webkitRelativePath', {
      value: relativePath.replace(/\.mht$/i, '.mhtml'),
      configurable: true,
    })
  }
  return normalized
}
