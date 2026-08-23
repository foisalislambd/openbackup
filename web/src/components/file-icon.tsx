import {
  File as FileIcon,
  FileArchive,
  FileAudio,
  FileCode,
  FileImage,
  FileSpreadsheet,
  FileText,
  FileVideo,
  Folder,
  Lock,
} from 'lucide-react'
import { fileKind, type FileKind } from '@/lib/file-kind'
import { cn } from '@/lib/cn'

const palette: Record<FileKind, string> = {
  folder: 'text-amber-500',
  image: 'text-emerald-500',
  pdf: 'text-red-500',
  video: 'text-purple-500',
  audio: 'text-pink-500',
  text: 'text-sky-500',
  code: 'text-indigo-500',
  archive: 'text-orange-500',
  spreadsheet: 'text-green-600',
  document: 'text-blue-500',
  other: 'text-gray-400',
}

function Glyph({ kind, size }: { kind: FileKind; size: number }) {
  const props = { size, strokeWidth: 1.75, className: 'shrink-0', 'aria-hidden': true as const }
  switch (kind) {
    case 'folder':
      return <Folder {...props} fill="currentColor" stroke="none" />
    case 'image':
      return <FileImage {...props} />
    case 'pdf':
      return <FileText {...props} />
    case 'video':
      return <FileVideo {...props} />
    case 'audio':
      return <FileAudio {...props} />
    case 'text':
      return <FileText {...props} />
    case 'code':
      return <FileCode {...props} />
    case 'archive':
      return <FileArchive {...props} />
    case 'spreadsheet':
      return <FileSpreadsheet {...props} />
    case 'document':
      return <FileText {...props} />
    default:
      return <FileIcon {...props} />
  }
}

export function EntryIcon({
  path,
  type,
  locked = false,
  large = false,
}: {
  path: string
  type?: string
  locked?: boolean
  large?: boolean
}) {
  const kind = fileKind(path, type)
  const size = large ? 40 : 28
  return (
    <span className={cn('relative inline-flex shrink-0', palette[kind])}>
      <Glyph kind={kind} size={size} />
      {locked && (
        <span
          className="absolute -right-1 -bottom-1 grid size-4 place-items-center rounded-full bg-gray-900 text-white shadow-sm dark:bg-gray-100 dark:text-gray-900"
          title="End-to-end encrypted — locked on this server"
        >
          <Lock size={10} strokeWidth={2.5} />
        </span>
      )}
    </span>
  )
}

export function LockBadge({ label = 'Locked' }: { label?: string }) {
  return (
    <span className="inline-flex items-center gap-1 rounded-md bg-gray-900/90 px-1.5 py-0.5 text-[10px] font-semibold tracking-wide text-white uppercase dark:bg-white/15">
      <Lock size={10} strokeWidth={2.5} />
      {label}
    </span>
  )
}
