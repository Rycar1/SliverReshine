import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import ConfirmDialog from '../ConfirmDialog'

describe('ConfirmDialog', () => {
  it('renders nothing when closed', () => {
    const { container } = render(
      <ConfirmDialog open={false} title="Delete" onConfirm={vi.fn()} onCancel={vi.fn()} />,
    )
    expect(container.firstChild).toBeNull()
  })

  it('renders title and children when open', () => {
    render(
      <ConfirmDialog open title="Delete item" onConfirm={vi.fn()} onCancel={vi.fn()}>
        Are you sure?
      </ConfirmDialog>,
    )
    expect(screen.getByRole('alertdialog')).toBeInTheDocument()
    expect(screen.getByText('Delete item')).toBeInTheDocument()
    expect(screen.getByText('Are you sure?')).toBeInTheDocument()
  })

  it('fires onConfirm when the confirm button is clicked', () => {
    const onConfirm = vi.fn()
    const onCancel = vi.fn()
    render(<ConfirmDialog open title="Delete" confirmLabel="Yes" onConfirm={onConfirm} onCancel={onCancel} />)
    fireEvent.click(screen.getByText('Yes'))
    expect(onConfirm).toHaveBeenCalledTimes(1)
    expect(onCancel).not.toHaveBeenCalled()
  })

  it('fires onCancel on backdrop click and Escape', () => {
    const onConfirm = vi.fn()
    const onCancel = vi.fn()
    const { container } = render(
      <ConfirmDialog open title="Delete" onConfirm={onConfirm} onCancel={onCancel} />,
    )
    fireEvent.click(container.querySelector('.modal-overlay')!)
    expect(onCancel).toHaveBeenCalledTimes(1)

    fireEvent.keyDown(window, { key: 'Escape' })
    expect(onCancel).toHaveBeenCalledTimes(2)
    expect(onConfirm).not.toHaveBeenCalled()
  })

  it('fires onConfirm on Enter when not busy', () => {
    const onConfirm = vi.fn()
    render(<ConfirmDialog open title="Delete" onConfirm={onConfirm} onCancel={vi.fn()} />)
    fireEvent.keyDown(window, { key: 'Enter' })
    expect(onConfirm).toHaveBeenCalledTimes(1)
  })

  it('disables buttons while busy and ignores Enter', () => {
    const onConfirm = vi.fn()
    render(<ConfirmDialog open busy title="Delete" onConfirm={onConfirm} onCancel={vi.fn()} />)
    fireEvent.keyDown(window, { key: 'Enter' })
    expect(onConfirm).not.toHaveBeenCalled()
    expect(screen.getAllByRole('button').every((b) => (b as HTMLButtonElement).disabled)).toBe(true)
  })
})
