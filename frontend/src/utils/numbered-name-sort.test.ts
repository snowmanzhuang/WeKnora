import assert from 'node:assert/strict'
import test from 'node:test'

import { compareNumberedNames, sortByNumberedName } from './numbered-name-sort'

test('sorts leading numbered names numerically and puts unnumbered names last', () => {
  const names = ['99 病例推理', '24 临床指南', '知识助手', '02 角膜', '00 汇总', '01 青光眼']

  assert.deepEqual(
    sortByNumberedName(names, (name) => name),
    ['00 汇总', '01 青光眼', '02 角膜', '24 临床指南', '99 病例推理', '知识助手'],
  )
})

test('recognizes a numeric prefix without a separating space', () => {
  assert.ok(compareNumberedNames('09号机器人', '10号机器人') < 0)
  assert.ok(compareNumberedNames('24号机器人', '未编号机器人') < 0)
})
