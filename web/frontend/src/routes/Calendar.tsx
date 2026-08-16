import { CalendarOff } from 'lucide-react'
import { PageHeader } from '../components/layout/PageHeader'
import { EmptyState } from '../components/ui/EmptyState'

// The calendar's release data came from a metadata provider that has been
// removed; the screen returns with the direct metadata-provider integration.
// Until then this is an honest empty state, not a blank page.
export function Calendar() {
  return (
    <>
      <PageHeader title="Calendar" subtitle="Upcoming and recent releases" />
      <div data-testid="calendar-no-provider">
        <EmptyState
          icon={CalendarOff}
          title="No metadata provider configured"
          hint="Release dates return when a metadata provider integration is set up."
        />
      </div>
    </>
  )
}
