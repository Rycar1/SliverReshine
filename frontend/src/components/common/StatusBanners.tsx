/**
 * The error / success banner pair the list pages render above their content.
 *
 * `message` deliberately reuses the error-banner class with the success
 * palette, so the two banners stay identical wherever they appear. The
 * stylesheet is owned by the page, matching the other components/common
 * entries, which also render classed markup without importing CSS.
 */
export default function StatusBanners({ error, message }: { error?: string; message?: string }) {
  return (
    <>
      {error && <div className="error-banner">{error}</div>}
      {message && (
        <div
          className="error-banner"
          style={{
            borderColor: 'var(--green)',
            color: 'var(--green)',
            background: 'var(--success-bg)',
          }}
        >
          {message}
        </div>
      )}
    </>
  )
}
