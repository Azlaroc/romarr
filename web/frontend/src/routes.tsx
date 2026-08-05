import { createBrowserRouter } from 'react-router-dom'
import App from './App'
import { Library } from './routes/Library'
import { AddNew } from './routes/AddNew'
import { Queue } from './routes/activity/Queue'
import { Wishlist } from './routes/wanted/Wishlist'
import { Settings } from './routes/settings/Settings'
import { NotFound } from './routes/NotFound'

export const router = createBrowserRouter([
  {
    path: '/',
    element: <App />,
    children: [
      { index: true, element: <Library /> },
      { path: 'add', element: <AddNew /> },
      { path: 'activity', element: <Queue /> },
      { path: 'wanted', element: <Wishlist /> },
      { path: 'settings', element: <Settings /> },
      { path: '*', element: <NotFound /> },
    ],
  },
])
