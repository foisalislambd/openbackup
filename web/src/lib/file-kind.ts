/** Guess a file's kind from its name so the browser can pick an icon and a preview. */

export type FileKind =
  | 'folder'
  | 'image'
  | 'pdf'
  | 'video'
  | 'audio'
  | 'text'
  | 'code'
  | 'archive'
  | 'spreadsheet'
  | 'document'
  | 'other'

const IMAGE = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'ico', 'avif', 'heic', 'heif'])
const VIDEO = new Set(['mp4', 'webm', 'mov', 'mkv', 'avi'])
const AUDIO = new Set(['mp3', 'wav', 'ogg', 'm4a', 'flac', 'aac'])
const TEXT = new Set(['txt', 'md', 'csv', 'log', 'json', 'xml', 'yml', 'yaml', 'ini', 'toml', 'env'])
const CODE = new Set([
  'js',
  'ts',
  'tsx',
  'jsx',
  'css',
  'html',
  'go',
  'py',
  'rs',
  'java',
  'c',
  'h',
  'cpp',
  'sh',
  'ps1',
  'sql',
])
const ARCHIVE = new Set(['zip', '7z', 'rar', 'tar', 'gz', 'tgz'])
const SHEET = new Set(['xls', 'xlsx', 'ods', 'numbers'])
const DOC = new Set(['doc', 'docx', 'odt', 'rtf', 'pages'])

export function extensionOf(name: string): string {
  const base = name.split(/[\\/]/).pop() ?? name
  const dot = base.lastIndexOf('.')
  if (dot <= 0) return ''
  return base.slice(dot + 1).toLowerCase()
}

export function fileKind(path: string, type?: string): FileKind {
  if (type === 'dir') return 'folder'
  const ext = extensionOf(path)
  if (IMAGE.has(ext)) return 'image'
  if (ext === 'pdf') return 'pdf'
  if (VIDEO.has(ext)) return 'video'
  if (AUDIO.has(ext)) return 'audio'
  if (TEXT.has(ext)) return 'text'
  if (CODE.has(ext)) return 'code'
  if (ARCHIVE.has(ext)) return 'archive'
  if (SHEET.has(ext)) return 'spreadsheet'
  if (DOC.has(ext)) return 'document'
  return 'other'
}

export function kindLabel(kind: FileKind): string {
  switch (kind) {
    case 'folder':
      return 'Folder'
    case 'image':
      return 'Image'
    case 'pdf':
      return 'PDF'
    case 'video':
      return 'Video'
    case 'audio':
      return 'Audio'
    case 'text':
      return 'Text'
    case 'code':
      return 'Code'
    case 'archive':
      return 'Archive'
    case 'spreadsheet':
      return 'Spreadsheet'
    case 'document':
      return 'Document'
    default:
      return 'File'
  }
}

export function canPreview(kind: FileKind, size?: number): boolean {
  if (kind === 'folder' || kind === 'archive' || kind === 'spreadsheet' || kind === 'document') return false
  if (kind === 'other') return false
  const max = kind === 'video' || kind === 'audio' ? 80 * 1024 * 1024 : 25 * 1024 * 1024
  if (size != null && size > max) return false
  return true
}

export function isEncryptedDevice(device?: { encrypted?: boolean; key_id?: string } | null): boolean {
  return Boolean(device?.encrypted || device?.key_id)
}
