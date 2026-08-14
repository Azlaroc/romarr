import { createBrowserRouter, Navigate } from 'react-router-dom'
import App from './App'
import { Library } from './routes/Library'
import { AddNew } from './routes/AddNew'
import { Calendar } from './routes/Calendar'
import { PlayLog } from './routes/PlayLog'
import { Queue } from './routes/activity/Queue'
import { History } from './routes/activity/History'
import { Blocklist } from './routes/activity/Blocklist'
import { Wishlist } from './routes/wanted/Wishlist'
import { Requests } from './routes/wanted/Requests'
import { Settings } from './routes/settings/Settings'
import { Profiles } from './routes/settings/Profiles'
import { QualityProfileEditor } from './routes/settings/QualityProfileEditor'
import { ReleaseProfileEditor } from './routes/settings/ReleaseProfileEditor'
import { NotFound } from './routes/NotFound'

export const router = createBrowserRouter([
  {
    path: '/',
    element: <App />,
    children: [
      { index: true, element: <Library /> },
      { path: 'add', element: <AddNew /> },
      { path: 'calendar', element: <Calendar /> },
      { path: 'playlog', element: <PlayLog /> },
      { path: 'activity', element: <Navigate to="/activity/queue" replace /> },
      { path: 'activity/queue', element: <Queue /> },
      { path: 'activity/history', element: <History /> },
      { path: 'activity/blocklist', element: <Blocklist /> },
      { path: 'wanted', element: <Navigate to="/wanted/wishlist" replace /> },
      { path: 'wanted/wishlist', element: <Wishlist /> },
      { path: 'wanted/requests', element: <Requests /> },
      { path: 'settings', element: <Navigate to="/settings/general" replace /> },
      { path: 'settings/general', element: <Settings /> },
      { path: 'settings/profiles', element: <Profiles /> },
      { path: 'settings/profiles/quality/new', element: <QualityProfileEditor /> },
      { path: 'settings/profiles/quality/:id', element: <QualityProfileEditor /> },
      { path: 'settings/profiles/release/new', element: <ReleaseProfileEditor /> },
      { path: 'settings/profiles/release/:id', element: <ReleaseProfileEditor /> },
      { path: '*', element: <NotFound /> },
    ],
  },
])
