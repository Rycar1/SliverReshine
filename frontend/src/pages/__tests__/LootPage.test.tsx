import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, waitFor } from '@testing-library/react'
import LootPage from '../LootPage'
import { api } from '../../lib/api'

vi.mock('../../lib/api', () => ({
  api: {
    lootList: vi.fn(),
  },
}))

const mockedApi = vi.mocked(api)

describe('LootPage credential names', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  // The vault has no name field for a credential, so a harvest is filed under
  // its collection -- which used to be the constant "ai-collect" for every
  // assistant finding, leaving the operator to open each row to learn what it
  // was. The list now leads with the finding's own label.
  it('shows what each credential is, not the collection it was filed under', async () => {
    mockedApi.lootList.mockResolvedValue({
      loot: [
        {
          ID: 'c1',
          Name: 'PostgreSQL superuser',
          LootType: 'LOOT_CREDENTIAL',
          FileType: '',
          File: '',
          Size: 0,
          CredUser: 'postgres',
          CredPassword: 'Pg_Pa55',
        },
        {
          ID: 'c2',
          Name: 'AWS access key (deploy)',
          LootType: 'LOOT_CREDENTIAL',
          FileType: '',
          File: '',
          Size: 0,
          CredUser: 'apikey',
          CredAPIKey: 'AKIA...',
        },
      ],
    })

    const { container } = render(<LootPage />)

    await waitFor(() => expect(container.textContent).toContain('PostgreSQL superuser'))
    // The username is the concrete identifier, so it rides along beside the label.
    expect(container.textContent).toContain('PostgreSQL superuser · postgres')
    expect(container.textContent).toContain('AWS access key (deploy)')
    // An API key is filed under the reserved "apikey" username; that marker is
    // not an account and must not be shown as one.
    expect(container.textContent).not.toContain('apikey')
    // The credential type is rendered in the row and in the type filter, so
    // assert presence rather than a unique match.
    expect(container.textContent).toContain('LOOT_CREDENTIAL')
  })
})
