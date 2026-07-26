/** Skeleton placeholders shown while a page's first load is in flight. */

function Bone({ className = '' }: { className?: string }) {
  return <div className={`skeleton-bone ${className}`} aria-hidden />
}

/** Generic block used for simple lists and nested cards. */
export function Skeleton({ className = '' }: { className?: string }) {
  return <Bone className={className} />
}

export function OverviewSkeleton() {
  return (
    <div className="space-y-8" role="status" aria-label="Loading overview">
      <Bone className="h-20 w-full rounded-2xl" />
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        {Array.from({ length: 4 }, (_, i) => (
          <div key={i} className="panel space-y-3 p-4">
            <Bone className="size-10 rounded-xl" />
            <Bone className="h-4 w-32 rounded" />
            <Bone className="h-3 w-24 rounded" />
          </div>
        ))}
      </div>
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        {Array.from({ length: 4 }, (_, i) => (
          <div key={i} className="panel px-5 py-4">
            <Bone className="h-3 w-16 rounded" />
            <Bone className="mt-3 h-7 w-24 rounded" />
            <Bone className="mt-2 h-3 w-28 rounded" />
          </div>
        ))}
      </div>
    </div>
  )
}

export function DevicesSkeleton() {
  return (
    <div className="space-y-4" role="status" aria-label="Loading devices">
      {Array.from({ length: 3 }, (_, i) => (
        <div key={i} className="flex flex-wrap items-start justify-between gap-3 py-4 first:pt-0">
          <div className="min-w-0 flex-1 space-y-2">
            <Bone className="h-4 w-36 rounded" />
            <Bone className="h-3 w-64 max-w-full rounded" />
            <Bone className="h-3 w-48 max-w-full rounded" />
          </div>
          <div className="flex gap-2">
            <Bone className="h-8 w-24 rounded-lg" />
            <Bone className="h-8 w-16 rounded-lg" />
            <Bone className="h-8 w-16 rounded-lg" />
          </div>
        </div>
      ))}
    </div>
  )
}

export function BackupsSkeleton() {
  return (
    <div
      className="rounded-xl border border-[var(--color-border-subtle)] bg-[var(--color-surface)]"
      role="status"
      aria-label="Loading backups"
    >
      <div className="border-b border-[var(--color-border-subtle)] px-5 py-3.5">
        <Bone className="h-4 w-24 rounded" />
      </div>
      <div className="divide-y divide-[var(--color-border-subtle)] px-5">
        {Array.from({ length: 5 }, (_, i) => (
          <div key={i} className="flex flex-wrap items-center justify-between gap-3 py-3">
            <div className="min-w-0 flex-1 space-y-2">
              <Bone className="h-4 w-52 max-w-full rounded" />
              <Bone className="h-3 w-40 max-w-full rounded" />
            </div>
            <div className="flex gap-2">
              <Bone className="h-8 w-16 rounded-lg" />
              <Bone className="h-8 w-24 rounded-lg" />
              <Bone className="h-8 w-16 rounded-lg" />
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}

export function BrowseSkeleton() {
  return (
    <div className="space-y-4" role="status" aria-label="Opening backup">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="space-y-2">
          <Bone className="h-4 w-56 rounded" />
          <Bone className="h-3 w-36 rounded" />
        </div>
        <div className="flex gap-2">
          <Bone className="h-8 w-40 rounded-lg" />
          <Bone className="h-8 w-24 rounded-lg" />
        </div>
      </div>
      <Bone className="h-4 w-64 max-w-full rounded" />
      <div className="rounded-xl border border-[var(--color-border-subtle)] bg-[var(--color-surface)] px-5 py-2">
        {Array.from({ length: 8 }, (_, i) => (
          <div key={i} className="flex items-center justify-between gap-3 py-2.5">
            <div className="flex items-center gap-2">
              <Bone className="size-4 rounded" />
              <Bone className="h-4 w-40 rounded" />
            </div>
            <Bone className="h-8 w-20 rounded-lg" />
          </div>
        ))}
      </div>
    </div>
  )
}

export function ActivitySkeleton() {
  return (
    <div
      className="rounded-xl border border-[var(--color-border-subtle)] bg-[var(--color-surface)]"
      role="status"
      aria-label="Loading activity"
    >
      <div className="border-b border-[var(--color-border-subtle)] px-5 py-3.5">
        <Bone className="h-4 w-20 rounded" />
      </div>
      <div className="space-y-0 px-5">
        {Array.from({ length: 8 }, (_, i) => (
          <div key={i} className="flex gap-3 border-b border-[var(--color-border-subtle)] py-3 last:border-0">
            <Bone className="mt-1.5 size-2 shrink-0 rounded-full" />
            <div className="min-w-0 flex-1 space-y-2">
              <Bone className="h-4 w-64 max-w-full rounded" />
              <Bone className="h-3 w-40 max-w-full rounded" />
            </div>
            <Bone className="h-3 w-16 shrink-0 rounded" />
          </div>
        ))}
      </div>
    </div>
  )
}

export function SettingsSkeleton() {
  return (
    <div className="space-y-6" role="status" aria-label="Loading settings">
      <div className="rounded-xl border border-[var(--color-border-subtle)] bg-[var(--color-surface)]">
        <div className="border-b border-[var(--color-border-subtle)] px-5 py-3.5">
          <Bone className="h-4 w-20 rounded" />
        </div>
        <div className="space-y-4 px-5 py-4">
          <Bone className="h-4 w-48 rounded" />
          <div className="grid gap-3 sm:grid-cols-[1fr_1fr_auto]">
            <Bone className="h-10 w-full rounded-lg" />
            <Bone className="h-10 w-full rounded-lg" />
            <Bone className="h-10 w-20 rounded-lg" />
          </div>
        </div>
      </div>
      <div className="rounded-xl border border-[var(--color-border-subtle)] bg-[var(--color-surface)]">
        <div className="border-b border-[var(--color-border-subtle)] px-5 py-3.5">
          <Bone className="h-4 w-28 rounded" />
        </div>
        <div className="grid gap-5 px-5 py-4 sm:grid-cols-2">
          {Array.from({ length: 4 }, (_, i) => (
            <div key={i} className="space-y-2">
              <Bone className="h-4 w-32 rounded" />
              <Bone className="h-10 w-full rounded-lg" />
              <Bone className="h-3 w-48 max-w-full rounded" />
            </div>
          ))}
        </div>
      </div>
      <div className="rounded-xl border border-[var(--color-border-subtle)] bg-[var(--color-surface)]">
        <div className="border-b border-[var(--color-border-subtle)] px-5 py-3.5">
          <Bone className="h-4 w-40 rounded" />
        </div>
        <div className="space-y-3 px-5 py-4">
          <Bone className="h-4 w-full rounded" />
          <Bone className="h-4 w-11/12 max-w-xl rounded" />
          <Bone className="h-4 w-72 max-w-full rounded" />
        </div>
      </div>
    </div>
  )
}

/** Compact shell placeholder while bootstrap runs. */
export function ShellSkeleton() {
  return (
    <div className="app-shell" role="status" aria-label="Starting">
      <aside className="app-sidebar">
        <div className="flex items-center gap-3 px-2">
          <Bone className="size-10 rounded-2xl" />
          <div className="space-y-2">
            <Bone className="h-4 w-28 rounded" />
            <Bone className="h-3 w-14 rounded" />
          </div>
        </div>
        <div className="space-y-2">
          {Array.from({ length: 5 }, (_, i) => (
            <Bone key={i} className="h-10 w-full rounded-xl" />
          ))}
        </div>
      </aside>
      <div className="app-main">
        <div className="app-topbar">
          <div className="space-y-2">
            <Bone className="h-7 w-40 rounded" />
            <Bone className="h-4 w-56 rounded" />
          </div>
        </div>
        <div className="app-content">
          <OverviewSkeleton />
        </div>
      </div>
    </div>
  )
}
