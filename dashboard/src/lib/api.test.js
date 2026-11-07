import { afterEach, describe, expect, it, vi } from 'vitest'
import { remove, save } from './api'

const answer = (status, body) =>
  vi.stubGlobal('fetch', () =>
    Promise.resolve({
      ok: status >= 200 && status < 300,
      status,
      json: () =>
        body === undefined
          ? Promise.reject(new Error('no body'))
          : Promise.resolve(body),
    }),
  )

afterEach(() => vi.unstubAllGlobals())

// A change either happened or was refused, and the console says which.
// The server names a refusal in the body, and has sent it with a 200.
describe('changing a link', () => {
  it('is a success when the server says nothing', async () => {
    answer(200)
    expect(await save('create', undefined, { alias: 'blog' })).toEqual({
      error: null,
      alias: null,
    })
  })

  it('exposes the alias a create stored when the server names one', async () => {
    answer(200, { alias: 'ab' })
    expect(await save('create', undefined, {})).toEqual({
      error: null,
      alias: 'ab',
    })
  })

  it('is a refusal when the server names one, whatever the status', async () => {
    answer(200, { message: 'alias already existed' })
    expect(await save('create', undefined, { alias: 'blog' })).toEqual({
      error: 'alias already existed',
      alias: null,
    })
    answer(400, { message: 'unsupported operator' })
    expect(await remove('blog')).toBe('unsupported operator')
  })

  it('names the status when a failure carries no message', async () => {
    answer(502)
    expect(await remove('blog')).toBe('request failed (502)')
  })
})
