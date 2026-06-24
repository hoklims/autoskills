import type { Stats } from '../lib/api'
import './Sidebar.scss'

export type View = 'inbox' | 'accepted' | 'rejected' | 'projects'

interface SidebarProps {
  view: View
  stats: Stats | null
  onNavigate: (view: View) => void
}

const NAV_ITEMS: Array<{ view: View; label: string; count: (s: Stats) => number }> = [
  { view: 'inbox', label: 'inbox', count: (s) => s.pending },
  { view: 'accepted', label: 'accepted', count: (s) => s.accepted },
  { view: 'rejected', label: 'rejected', count: (s) => s.rejected },
  { view: 'projects', label: 'projects', count: (s) => s.projects },
]

export function Sidebar({ view, stats, onNavigate }: SidebarProps) {
  return (
    <nav className="sidebar" aria-label="sections">
      <ul className="sidebar__list">
        {NAV_ITEMS.map((item) => (
          <li key={item.view}>
            <button
              type="button"
              className={`sidebar__item${view === item.view ? ' sidebar__item--active' : ''}`}
              aria-current={view === item.view ? 'page' : undefined}
              onClick={() => onNavigate(item.view)}
            >
              <span>{item.label}</span>
              {stats && <span className="sidebar__count">{item.count(stats)}</span>}
            </button>
          </li>
        ))}
      </ul>
    </nav>
  )
}
