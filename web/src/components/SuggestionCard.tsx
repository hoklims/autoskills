import { useState } from 'react'
import type { DecisionAction, Suggestion } from '../lib/api'
import './SuggestionCard.scss'

interface SuggestionCardProps {
  suggestion: Suggestion
  leaving: boolean
  readonly?: boolean
  onDecision: (action: DecisionAction, body?: string) => void
}

export function SuggestionCard({ suggestion, leaving, readonly = false, onDecision }: SuggestionCardProps) {
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(suggestion.body)

  const pct = Math.round(Math.min(Math.max(suggestion.confidence, 0), 1) * 100)
  // only send the body field when the user actually changed it
  const editedBody = draft !== suggestion.body ? draft : undefined

  return (
    <article className={`suggestion-card${leaving ? ' suggestion-card--leaving' : ''}`}>
      <div className="suggestion-card__badges">
        <span className="suggestion-card__badge">{suggestion.signal.replace('_', ' ')}</span>
        <span className="suggestion-card__badge">{suggestion.scope}</span>
        <span className="suggestion-card__badge">{suggestion.placement.replace('_', ' ')}</span>
        {suggestion.sensitivity && (
          <span className="suggestion-card__badge suggestion-card__badge--inverted">sensitive</span>
        )}
        <span className="suggestion-card__project">{suggestion.project}</span>
      </div>

      <h3 className="suggestion-card__title">{suggestion.title}</h3>
      <p className="suggestion-card__rationale">{suggestion.rationale}</p>

      {suggestion.evidence.length > 0 && (
        <div className="suggestion-card__evidence-list">
          {suggestion.evidence.map((ev, i) => (
            <figure key={`${ev.sessionId}-${i}`} className="suggestion-card__evidence">
              <figcaption className="suggestion-card__evidence-label">evidence — {ev.tool}</figcaption>
              <blockquote className="suggestion-card__excerpt">{ev.excerpt}</blockquote>
            </figure>
          ))}
        </div>
      )}

      <div className="suggestion-card__target">
        <span className="suggestion-card__target-label">target</span>
        <code className="suggestion-card__target-path">{suggestion.targetPath}</code>
      </div>

      {editing ? (
        <textarea
          className="input-base suggestion-card__editor"
          value={draft}
          rows={Math.max(6, draft.split('\n').length + 1)}
          onChange={(e) => setDraft(e.target.value)}
          aria-label="skill body"
        />
      ) : (
        <pre className="suggestion-card__body">{editedBody ?? suggestion.body}</pre>
      )}

      <div className="suggestion-card__footer">
        <div
          className="suggestion-card__confidence"
          role="img"
          aria-label={`confidence ${pct} percent`}
        >
          <span className="suggestion-card__confidence-label">conf {pct}%</span>
          <span className="suggestion-card__meter">
            <span className="suggestion-card__meter-fill" style={{ width: `${pct}%` }} />
          </span>
        </div>
        {!readonly && (
          <div className="suggestion-card__actions">
            <button
              type="button"
              className="suggestion-card__reject"
              disabled={leaving}
              onClick={() => onDecision('reject')}
            >
              reject
            </button>
            <button
              type="button"
              className="btn-secondary suggestion-card__btn"
              onClick={() => setEditing((v) => !v)}
            >
              {editing ? 'done' : 'edit'}
            </button>
            <button
              type="button"
              className="btn-primary suggestion-card__btn"
              disabled={leaving}
              onClick={() => onDecision('accept', editedBody)}
            >
              accept
            </button>
          </div>
        )}
      </div>
    </article>
  )
}
