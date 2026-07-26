import { Route, Routes } from 'react-router-dom'

import { Shell } from '@/components/shell'
import ActivityPage from '@/pages/activity'
import BackupsPage from '@/pages/backups'
import DevicesPage from '@/pages/devices'
import NotFoundPage from '@/pages/not-found'
import OverviewPage from '@/pages/overview'
import SettingsPage from '@/pages/settings'

export default function App() {
  return (
    <Shell>
      <Routes>
        <Route path="/" element={<OverviewPage />} />
        <Route path="/devices" element={<DevicesPage />} />
        <Route path="/backups" element={<BackupsPage />} />
        <Route path="/activity" element={<ActivityPage />} />
        <Route path="/settings" element={<SettingsPage />} />
        <Route path="*" element={<NotFoundPage />} />
      </Routes>
    </Shell>
  )
}
