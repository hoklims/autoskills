import './EmptyState.scss'

interface EmptyStateProps {
  hint: string
  message: string
}

export function EmptyState({ hint, message }: EmptyStateProps) {
  return (
    <div className="empty-state">
      <span className="empty-state__hint">{hint}</span>
      <p className="empty-state__message">{message}</p>
    </div>
  )
}
