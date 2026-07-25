import type { Metadata } from 'next'
import './globals.css'
import { Shell } from '@/components/shell'

export const metadata: Metadata = {
  title: 'OpenBackup',
  description: 'Your files, backed up automatically to a server you control.',
}

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body className="min-h-screen">
        <Shell>{children}</Shell>
      </body>
    </html>
  )
}
