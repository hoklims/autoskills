import type { Stats } from '../lib/api'
import './StatBar.scss'

interface StatBarProps {
  stats: Stats
}

const STAT_ORDER: Array<{ key: keyof Stats; label: string }> = [
  { key: 'pending', label: 'pending' },
  { key: 'accepted', label: 'accepted' },
  { key: 'rejected', label: 'rejected' },
  { key: 'sessions', label: 'sessions' },
  { key: 'projects', label: 'projects' },
]

export function StatBar({ stats }: StatBarProps) {
  return (
    <div className="stat-bar" role="status">
      {STAT_ORDER.map(({ key, label }) => (
        <span key={key} className="stat-bar__pill">
          <span className="stat-bar__label">{label}</span>
          <span className="stat-bar__count">{stats[key]}</span>
        </span>
      ))}
    </div>
  )
}
