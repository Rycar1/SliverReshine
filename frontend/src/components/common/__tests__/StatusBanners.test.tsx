import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import StatusBanners from '../StatusBanners'

describe('StatusBanners', () => {
  it('renders nothing without a message', () => {
    const { container } = render(<StatusBanners />)
    expect(container.firstChild).toBeNull()
  })

  it('renders the error alone', () => {
    render(<StatusBanners error="boom" />)
    expect(screen.getByText('boom')).toHaveClass('error-banner')
  })

  it('renders the success message with the green palette', () => {
    render(<StatusBanners message="done" />)
    const el = screen.getByText('done')
    expect(el).toHaveClass('error-banner')
    expect(el).toHaveStyle({ color: 'var(--green)', background: 'var(--success-bg)' })
  })

  it('renders both banners at once', () => {
    render(<StatusBanners error="boom" message="done" />)
    expect(screen.getByText('boom')).toBeInTheDocument()
    expect(screen.getByText('done')).toBeInTheDocument()
  })
})
