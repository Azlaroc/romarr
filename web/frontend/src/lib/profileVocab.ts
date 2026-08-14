// Mirrors internal/api/quality.go validRegionTokens / validFormatTokens.
// The backend rejects unknown tokens (400) — keep these lists in sync by hand.
export const REGION_TOKENS = [
  'usa',
  'world',
  'europe',
  'japan',
  'uk',
  'australia',
  'canada',
  'korea',
  'china',
  'taiwan',
  'france',
  'germany',
  'spain',
  'italy',
  'netherlands',
  'sweden',
  'brazil',
  'asia',
] as const

export const FORMAT_TOKENS = ['chd', 'cue', 'iso', 'gdi', 'zip', '7z', 'raw'] as const
