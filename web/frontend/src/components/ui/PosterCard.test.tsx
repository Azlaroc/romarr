import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { PosterCard } from './PosterCard'
import { ArtImage } from './ArtImage'
import { JumpRail } from './JumpRail'

describe('PosterCard', () => {
  it('renders the accent placeholder when no art source is given', () => {
    render(
      <PosterCard variant="photo" title="NES" accent="#d0312d" artSources={[]} testId="card-nes" />,
    )
    // Placeholder = the initial letter, never a broken image.
    expect(screen.getByText('N')).toBeInTheDocument()
    expect(screen.queryByRole('img')).not.toBeInTheDocument()
  })

  it('renders chips and forwards clicks', () => {
    const onClick = vi.fn()
    render(
      <PosterCard
        variant="poster"
        title="Alleyway"
        artSources={['/api/art/title/1']}
        chips={<span data-testid="chip-owned">owned</span>}
        onClick={onClick}
        testId="card-1"
      />,
    )
    expect(screen.getByTestId('chip-owned')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('card-1'))
    expect(onClick).toHaveBeenCalledOnce()
  })
})

describe('ArtImage', () => {
  it('walks the fallback chain on error and lands on the placeholder', () => {
    render(
      <ArtImage
        sources={['/a.png', '/b.png']}
        placeholder={<span data-testid="ph">placeholder</span>}
        testId="art"
      />,
    )
    fireEvent.error(screen.getByTestId('art')) // /a.png fails → /b.png
    expect(screen.getByTestId('art')).toHaveAttribute('src', '/b.png')
    fireEvent.error(screen.getByTestId('art')) // /b.png fails → placeholder
    expect(screen.getByTestId('ph')).toBeInTheDocument()
    expect(screen.queryByTestId('art')).not.toBeInTheDocument()
  })
})

describe('JumpRail', () => {
  it('renders only the letters the server reports and fires their offsets', () => {
    const onJump = vi.fn()
    render(
      <JumpRail
        letters={[
          { letter: '#', count: 679, offset: 0 },
          { letter: 'A', count: 1200, offset: 679 },
          { letter: 'Z', count: 90, offset: 17000 },
        ]}
        onJump={onJump}
      />,
    )
    expect(screen.queryByText('B')).not.toBeInTheDocument() // zero-count hidden
    fireEvent.click(screen.getByTestId('jump-rail-A'))
    expect(onJump).toHaveBeenCalledWith(679)
    fireEvent.click(screen.getByTestId('jump-rail-num'))
    expect(onJump).toHaveBeenCalledWith(0)
  })

  it('renders nothing for a single bucket', () => {
    const { container } = render(<JumpRail letters={[{ letter: 'A', count: 3, offset: 0 }]} onJump={() => {}} />)
    expect(container.firstChild).toBeNull()
  })
})
