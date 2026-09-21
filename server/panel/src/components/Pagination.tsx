import { ChevronLeft, ChevronRight } from 'lucide-react'
import { pageSizeOptions } from '../paging'

// Bir tablo kartının ÜSTÜNE konan sayfalama çubuğu (uzun tablolarda altta kalıp gözden
// kaçmaması için): "X–Y / toplam" özeti + sayfa başına gösterilecek satır sayısı + ileri/geri.
// total 0 ise hiçbir şey render etmez (EmptyState zaten yeterli).
export function Pagination({
  page,
  pageSize,
  total,
  onPageChange,
  onPageSizeChange,
}: {
  page: number
  pageSize: number
  total: number
  onPageChange: (page: number) => void
  onPageSizeChange: (size: number) => void
}) {
  if (total === 0) return null

  const pageCount = Math.max(1, Math.ceil(total / pageSize))
  const from = (page - 1) * pageSize + 1
  const to = Math.min(page * pageSize, total)

  return (
    <div className="pagination">
      <div className="pagination-summary">
        <span>
          {from}–{to} / {total}
        </span>
        <label className="pagination-size">
          Sayfa başına
          <select value={pageSize} onChange={(e) => onPageSizeChange(Number(e.target.value))} aria-label="Sayfa başına satır sayısı">
            {pageSizeOptions(pageSize).map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
        </label>
      </div>
      {pageCount > 1 && (
        <div className="pagination-controls">
          <button className="icon-btn" type="button" disabled={page <= 1} onClick={() => onPageChange(page - 1)} aria-label="Önceki sayfa">
            <ChevronLeft size={16} strokeWidth={1.9} />
          </button>
          <span className="pagination-page">
            Sayfa {page} / {pageCount}
          </span>
          <button
            className="icon-btn"
            type="button"
            disabled={page >= pageCount}
            onClick={() => onPageChange(page + 1)}
            aria-label="Sonraki sayfa"
          >
            <ChevronRight size={16} strokeWidth={1.9} />
          </button>
        </div>
      )}
    </div>
  )
}
