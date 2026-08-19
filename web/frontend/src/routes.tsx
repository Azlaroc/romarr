import { createBrowserRouter, Navigate } from 'react-router-dom'
import App from './App'
import { Library } from './routes/Library'
import { LibraryRename } from './routes/library/Rename'
import { AddNew } from './routes/AddNew'
import { Platforms } from './routes/Platforms'
import { Calendar } from './routes/Calendar'
import { PlayLog } from './routes/PlayLog'
import { Queue } from './routes/activity/Queue'
import { History } from './routes/activity/History'
import { Blocklist } from './routes/activity/Blocklist'
import { Collection } from './routes/wanted/Collection'
import { Wishlist } from './routes/wanted/Wishlist'
import { Settings } from './routes/settings/Settings'
import { MediaManagement } from './routes/settings/MediaManagement'
import { Indexers } from './routes/settings/Indexers'
import { DownloadClients } from './routes/settings/DownloadClients'
import { Connect } from './routes/settings/Connect'
import { Metadata } from './routes/settings/Metadata'
import { Tags } from './routes/settings/Tags'
import { Profiles } from './routes/settings/Profiles'
import { QualityDefinitions } from './routes/settings/QualityDefinitions'
import { QualityProfileEditor } from './routes/settings/QualityProfileEditor'
import { ReleaseProfileEditor } from './routes/settings/ReleaseProfileEditor'
import { SystemStatus } from './routes/system/Status'
import { Tasks } from './routes/system/Tasks'
import { Users } from './routes/system/Users'
import { Backup } from './routes/system/Backup'
import { NotFound } from './routes/NotFound'

export const router = createBrowserRouter([
  {
    path: '/',
    element: <App />,
    children: [
      { index: true, element: <Library /> },
      { path: 'library/rename', element: <LibraryRename /> },
      { path: 'add', element: <AddNew /> },
      { path: 'calendar', element: <Calendar /> },
      { path: 'platforms', element: <Platforms /> },
      { path: 'playlog', element: <PlayLog /> },
      { path: 'activity', element: <Navigate to="/activity/queue" replace /> },
      { path: 'activity/queue', element: <Queue /> },
      { path: 'activity/history', element: <History /> },
      { path: 'activity/blocklist', element: <Blocklist /> },
      { path: 'wanted', element: <Navigate to="/wanted/wishlist" replace /> },
      { path: 'wanted/wishlist', element: <Wishlist /> },
      { path: 'wanted/collection', element: <Collection /> },
      { path: 'settings', element: <Navigate to="/settings/media-management" replace /> },
      { path: 'settings/media-management', element: <MediaManagement /> },
      { path: 'settings/indexers', element: <Indexers /> },
      { path: 'settings/download-clients', element: <DownloadClients /> },
      { path: 'settings/connect', element: <Connect /> },
      { path: 'settings/metadata', element: <Metadata /> },
      { path: 'settings/tags', element: <Tags /> },
      { path: 'settings/general', element: <Settings /> },
      { path: 'settings/profiles', element: <Profiles /> },
      { path: 'settings/quality-definitions', element: <QualityDefinitions /> },
      { path: 'settings/profiles/quality/new', element: <QualityProfileEditor /> },
      { path: 'settings/profiles/quality/:id', element: <QualityProfileEditor /> },
      { path: 'settings/profiles/release/new', element: <ReleaseProfileEditor /> },
      { path: 'settings/profiles/release/:id', element: <ReleaseProfileEditor /> },
      { path: 'system', element: <Navigate to="/system/status" replace /> },
      { path: 'system/status', element: <SystemStatus /> },
      { path: 'system/tasks', element: <Tasks /> },
      { path: 'system/users', element: <Users /> },
      { path: 'system/backup', element: <Backup /> },
      { path: '*', element: <NotFound /> },
    ],
  },
])
