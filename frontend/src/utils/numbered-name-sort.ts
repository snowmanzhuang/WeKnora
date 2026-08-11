const numberedPrefixPattern = /^\s*(\d+)(?=\D|$)/

const nameCollator = new Intl.Collator('zh-CN', {
  numeric: true,
  sensitivity: 'base',
})

function getNumberedPrefix(name: string): number | null {
  const match = name.match(numberedPrefixPattern)
  if (!match) return null

  const prefix = Number(match[1])
  return Number.isSafeInteger(prefix) ? prefix : null
}

/**
 * Sort numbered resources first by their leading number (00, 01, ... 99),
 * then place resources without a leading number after them by display name.
 */
export function compareNumberedNames(aName = '', bName = ''): number {
  const aPrefix = getNumberedPrefix(aName)
  const bPrefix = getNumberedPrefix(bName)

  if (aPrefix !== null && bPrefix !== null && aPrefix !== bPrefix) {
    return aPrefix - bPrefix
  }
  if (aPrefix !== null && bPrefix === null) return -1
  if (aPrefix === null && bPrefix !== null) return 1

  return nameCollator.compare(aName, bName)
}

export function sortByNumberedName<T>(
  items: readonly T[],
  getName: (item: T) => string,
): T[] {
  return [...items].sort((a, b) => compareNumberedNames(getName(a), getName(b)))
}
