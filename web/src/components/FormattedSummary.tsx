import { Fragment, useMemo, useState, type ReactNode } from 'react'
import { ChevronDown, ChevronUp } from 'lucide-react'
import { cn } from '@/lib/utils'

interface FormattedSummaryProps {
  text: string
  maxChars?: number
  expanded?: boolean
  onToggleExpand?: () => void
  className?: string
}

type Block =
  | { type: 'heading'; text: string }
  | { type: 'ul'; items: string[] }
  | { type: 'ol'; items: string[] }
  | { type: 'paragraph'; text: string }

function normalizeInlineBullets(raw: string): string {
  return raw
    .replace(/\r\n/g, '\n')
    .split('\n')
    .map((line) => {
      const bulletMatches = line.match(/(?:^|\s)•\s+/g)
      if (bulletMatches && bulletMatches.length >= 2) {
        return line.replace(/\s+•\s+/g, '\n• ')
      }
      if (/[:.!?]\s+•\s+/.test(line)) {
        return line.replace(/([:.!?])\s+•\s+/g, '$1\n• ')
      }
      return line
    })
    .join('\n')
}

function parseBlocks(raw: string): Block[] {
  const normalized = normalizeInlineBullets(raw).trim()
  if (!normalized) return []

  const lines = normalized.split('\n')
  const blocks: Block[] = []
  let paragraphLines: string[] = []
  let ulItems: string[] = []
  let olItems: string[] = []

  const flushParagraph = () => {
    if (paragraphLines.length > 0) {
      blocks.push({ type: 'paragraph', text: paragraphLines.join(' ') })
      paragraphLines = []
    }
  }

  const flushUl = () => {
    if (ulItems.length > 0) {
      blocks.push({ type: 'ul', items: ulItems })
      ulItems = []
    }
  }

  const flushOl = () => {
    if (olItems.length > 0) {
      blocks.push({ type: 'ol', items: olItems })
      olItems = []
    }
  }

  const flushAll = () => {
    flushParagraph()
    flushUl()
    flushOl()
  }

  for (const rawLine of lines) {
    const line = rawLine.trim()
    if (!line) {
      flushAll()
      continue
    }

    const headingMatch = line.match(/^#{1,3}\s+(.+)$/)
    if (headingMatch) {
      flushAll()
      blocks.push({ type: 'heading', text: headingMatch[1] })
      continue
    }

    const ulMatch = line.match(/^(?:•|-|\*)\s+(.+)$/)
    if (ulMatch) {
      flushParagraph()
      flushOl()
      ulItems.push(ulMatch[1])
      continue
    }

    const olMatch = line.match(/^\d+[.)]\s+(.+)$/)
    if (olMatch) {
      flushParagraph()
      flushUl()
      olItems.push(olMatch[1])
      continue
    }

    flushUl()
    flushOl()
    paragraphLines.push(line)
  }

  flushAll()
  return blocks
}

function renderInline(text: string): ReactNode[] {
  const nodes: ReactNode[] = []
  const tokenRegex = /(\*\*[^*]+\*\*|__[^_]+__|`[^`]+`|\*[^*\s][^*]*\*)/g
  let lastIndex = 0
  let match: RegExpExecArray | null
  let key = 0

  while ((match = tokenRegex.exec(text)) !== null) {
    if (match.index > lastIndex) {
      nodes.push(text.slice(lastIndex, match.index))
    }

    const token = match[0]
    if ((token.startsWith('**') && token.endsWith('**')) || (token.startsWith('__') && token.endsWith('__'))) {
      nodes.push(
        <strong key={key++} className="font-semibold text-[#E4E4EA]">
          {token.slice(2, -2)}
        </strong>,
      )
    } else if (token.startsWith('`') && token.endsWith('`')) {
      nodes.push(
        <code key={key++} className="px-1 py-0.5 rounded bg-[#121215] text-[#E4E4EA] text-[12px] font-mono">
          {token.slice(1, -1)}
        </code>,
      )
    } else if (token.startsWith('*') && token.endsWith('*')) {
      nodes.push(
        <em key={key++} className="italic text-[#C8C8D4]">
          {token.slice(1, -1)}
        </em>,
      )
    } else {
      nodes.push(token)
    }

    lastIndex = match.index + token.length
  }

  if (lastIndex < text.length) {
    nodes.push(text.slice(lastIndex))
  }

  return nodes.map((node, idx) => <Fragment key={idx}>{node}</Fragment>)
}

function truncateText(text: string, maxChars: number): string {
  if (text.length <= maxChars) return text
  let sliced = text.slice(0, maxChars).replace(/\s+\S*$/, '') || text.slice(0, maxChars)

  // Avoid leaving an unclosed markdown token (e.g. "**Macro...") after truncation.
  for (const marker of ['**', '__', '`']) {
    const parts = sliced.split(marker)
    if (parts.length % 2 === 0) {
      const lastIdx = sliced.lastIndexOf(marker)
      if (lastIdx > 0) {
        sliced = sliced.slice(0, lastIdx).trimEnd()
      }
    }
  }

  // Drop a trailing empty list marker if the item text was trimmed away.
  sliced = sliced.replace(/\n(?:•|-|\*|\d+[.)])\s*$/, '').trimEnd()
  return `${sliced}...`
}

export function FormattedSummary({
  text,
  maxChars,
  expanded: controlledExpanded,
  onToggleExpand,
  className,
}: FormattedSummaryProps) {
  const [internalExpanded, setInternalExpanded] = useState(false)
  const isControlled = controlledExpanded !== undefined
  const isExpanded = isControlled ? controlledExpanded : internalExpanded

  const isLong = typeof maxChars === 'number' && text.length > maxChars
  const visibleText = isLong && !isExpanded ? truncateText(text, maxChars) : text

  const blocks = useMemo(() => parseBlocks(visibleText), [visibleText])

  const handleToggle = () => {
    if (onToggleExpand) {
      onToggleExpand()
    } else {
      setInternalExpanded((prev) => !prev)
    }
  }

  return (
    <div className={cn('space-y-2', className)}>
      <div className="space-y-2 text-[13px] text-[#A0A0AB] opacity-80 leading-relaxed">
        {blocks.map((block, idx) => {
          if (block.type === 'heading') {
            return (
              <div key={idx} className="text-[13px] font-semibold text-white pt-1">
                {renderInline(block.text)}
              </div>
            )
          }
          if (block.type === 'ul') {
            return (
              <ul key={idx} className="space-y-1 pl-4 list-disc marker:text-[#6B6B76]">
                {block.items.map((item, itemIdx) => (
                  <li key={itemIdx}>{renderInline(item)}</li>
                ))}
              </ul>
            )
          }
          if (block.type === 'ol') {
            return (
              <ol key={idx} className="space-y-1 pl-4 list-decimal marker:text-[#6B6B76]">
                {block.items.map((item, itemIdx) => (
                  <li key={itemIdx}>{renderInline(item)}</li>
                ))}
              </ol>
            )
          }
          return <p key={idx}>{renderInline(block.text)}</p>
        })}
      </div>

      {isLong && (
        <button
          type="button"
          onClick={handleToggle}
          className="inline-flex items-center gap-1 text-[12px] font-medium text-white hover:underline cursor-pointer"
        >
          <span>{isExpanded ? 'Read less' : 'Read more'}</span>
          {isExpanded ? (
            <ChevronUp className="w-3.5 h-3.5" />
          ) : (
            <ChevronDown className="w-3.5 h-3.5" />
          )}
        </button>
      )}
    </div>
  )
}
