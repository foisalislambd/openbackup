import { Navigate, Route, Routes, useLocation } from 'react-router-dom'

import { DashboardLayout } from '@/components/layout/dashboard-layout'
import DevicesPage from '@/pages/devices-page'
import FilesPage from '@/pages/files-page'
import HomePage from '@/pages/home-page'
import LogsPage from '@/pages/logs-page'
import NotFoundPage from '@/pages/not-found-page'
import SettingsPage from '@/pages/settings-page'

/** Keep old /backups and /activity URLs working with query strings intact. */
function LegacyRedirect({ to }: { to: string }) {
  const { search } = useLocation()
  return <Navigate to={`${to}${search}`} replace />
}

export default function App() {
  return (
    <DashboardLayout>
      <Routes>
        <Route path="/" element={<HomePage />} />
        <Route path="/files" element={<FilesPage />} />
        <Route path="/devices" element={<DevicesPage />} />
        <Route path="/logs" element={<LogsPage />} />
        <Route path="/settings" element={<SettingsPage />} />
        <Route path="/backups" element={<LegacyRedirect to="/files" />} />
        <Route path="/activity" element={<LegacyRedirect to="/logs" />} />
        <Route path="*" element={<NotFoundPage />} />
      </Routes>
    </DashboardLayout>
  )
}
