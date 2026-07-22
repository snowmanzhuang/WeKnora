import assert from 'node:assert/strict'
import test from 'node:test'

import { normalizeUploadFile } from './uploadFileNormalization'

test('normalizes a Windows .MHT short filename to .mhtml without changing data', async () => {
  const source = new File(['MHTML payload'], 'ALBERT~2.MHT', {
    type: 'multipart/related',
    lastModified: 123,
  })

  const normalized = normalizeUploadFile(source)

  assert.equal(normalized.name, 'ALBERT~2.mhtml')
  assert.equal(normalized.type, source.type)
  assert.equal(normalized.lastModified, source.lastModified)
  assert.equal(await normalized.text(), await source.text())
})
