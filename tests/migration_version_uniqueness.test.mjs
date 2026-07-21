import assert from 'node:assert/strict';
import { readdirSync } from 'node:fs';
import { resolve } from 'node:path';
import test from 'node:test';

const migrationsDir = resolve(import.meta.dirname, '..', 'migrations', 'versioned');
const migrationPattern = /^(\d{6})_(.+)\.(up|down)\.sql$/;

test('versioned migrations have one name per numeric version and matching directions', () => {
  const entries = new Map();

  for (const file of readdirSync(migrationsDir)) {
    const match = migrationPattern.exec(file);
    if (!match) continue;

    const [, version, name, direction] = match;
    const entry = entries.get(version) ?? { name, directions: new Set() };

    assert.equal(
      entry.name,
      name,
      `migration version ${version} is shared by ${entry.name} and ${name}`,
    );
    assert.ok(
      !entry.directions.has(direction),
      `migration ${version}_${name} has more than one ${direction} file`,
    );

    entry.directions.add(direction);
    entries.set(version, entry);
  }

  for (const [version, entry] of entries) {
    assert.deepEqual(
      [...entry.directions].sort(),
      ['down', 'up'],
      `migration ${version}_${entry.name} must provide matching up and down files`,
    );
  }
});
