import assert from 'node:assert'
import { describe, it } from 'node:test'
import type { Combo, ProviderConnection, ProviderNode } from '../../api/client'
import {
  resolveFilteredCombos,
  resolveFilteredGroups,
  resolveModelPickerGroups,
} from './pickerData'

describe('pickerData', () => {
  it('hides hidden providers from combo model picker even when connection is active', () => {
    const groups = resolveModelPickerGroups(
      [
        {
          id: 'conn-dahl',
          provider: 'dahl',
          authType: 'apikey',
          name: 'Dahl',
          email: null,
          priority: 1,
          isActive: 1,
          data: '{}',
          createdAt: '',
          updatedAt: '',
        },
      ],
      []
    )

    assert.strictEqual(groups.find((group) => group.id === 'dahl'), undefined)
  })

  it('includes connected providers and no-auth providers; inactive connections are hidden', () => {
    const connections: ProviderConnection[] = [
      {
        id: 'conn-1',
        provider: 'antigravity',
        authType: 'oauth',
        name: 'My Antigravity',
        email: null,
        priority: 1,
        isActive: 1,
        data: '{}',
        createdAt: '',
        updatedAt: '',
      },
      {
        id: 'conn-2',
        provider: 'claude',
        authType: 'oauth',
        name: 'My Claude',
        email: null,
        priority: 1,
        isActive: 0,
        data: '{}',
        createdAt: '',
        updatedAt: '',
      },
    ]

    const groups = resolveModelPickerGroups(connections, [])

    const agGroup = groups.find((g) => g.id === 'antigravity')
    assert.ok(agGroup)
    assert.ok(agGroup.models.length > 0)
    assert.strictEqual(agGroup.models[0].value.startsWith('ag/'), true)

    const ccGroup = groups.find((g) => g.id === 'claude')
    // Deliberate NOX9GO divergence from upstream (ModelSelectModal.js):
    // an inactive connection is not selectable in this picker.
    assert.strictEqual(ccGroup, undefined)

    // When active connections exist, unconnected noAuth providers stay hidden
    const ocGroup = groups.find((g) => g.id === 'opencode')
    assert.strictEqual(ocGroup, undefined)

    // With 0 active connections and no disabled rows, noAuth providers fall back in
    const emptyGroups = resolveModelPickerGroups([], [])
    assert.ok(emptyGroups.find((g) => g.id === 'opencode'))

    // When a noAuth provider has only inactive (isActive === 0) connection rows, it is hidden even with 0 active connections
    const disabledNoAuthGroups = resolveModelPickerGroups(
      [
        {
          id: 'conn-oc',
          provider: 'opencode',
          authType: 'none',
          name: 'OpenCode',
          email: null,
          priority: 1,
          isActive: 0,
          data: '{}',
          createdAt: '',
          updatedAt: '',
        },
      ],
      []
    )
    assert.strictEqual(disabledNoAuthGroups.find((g) => g.id === 'opencode'), undefined)
  })

  it('includes custom models with display names and respects prefix-qualified disabledModels', () => {
    const connections: ProviderConnection[] = [
      {
        id: 'conn-ag',
        provider: 'antigravity',
        authType: 'oauth',
        name: 'My Antigravity',
        email: null,
        priority: 1,
        isActive: 1,
        data: '{}',
        createdAt: '',
        updatedAt: '',
      },
    ]

    const groups = resolveModelPickerGroups(connections, [], {
      customModels: [
        {
          providerAlias: 'ag',
          id: 'gemini-4-carbon-medium',
          name: 'Gemini 4 Carbon (Medium)',
          type: 'llm',
        },
        {
          providerAlias: 'antigravity',
          id: 'gemini-4-hydrogen-medium',
          name: 'Gemini 4 Hydrogen (Medium)',
          type: 'llm',
        },
      ],
      disabledModels: {
        ag: ['ag/gemini-4-hydrogen-medium'],
      },
    })

    const agGroup = groups.find((g) => g.id === 'antigravity')
    assert.ok(agGroup)
    const carbon = agGroup.models.find((m) => m.id === 'gemini-4-carbon-medium')
    assert.ok(carbon, 'custom model with display name must be included')
    assert.strictEqual(carbon.value, 'ag/gemini-4-carbon-medium')
    assert.strictEqual(
      agGroup.models.some((m) => m.id === 'gemini-4-hydrogen-medium'),
      false,
      'prefix-qualified disabled model must be excluded'
    )
  })

  it('excludes pure media providers and non-llm models from model picker groups', () => {
    const connections: ProviderConnection[] = [
      {
        id: 'c-el',
        provider: 'elevenlabs',
        authType: 'apikey',
        name: null,
        email: null,
        priority: null,
        isActive: 1,
        data: '{}',
        createdAt: '',
        updatedAt: '',
      },
      {
        id: 'c-oai',
        provider: 'openai',
        authType: 'apikey',
        name: null,
        email: null,
        priority: null,
        isActive: 1,
        data: '{}',
        createdAt: '',
        updatedAt: '',
      },
    ]

    const groups = resolveModelPickerGroups(connections, [])
    const elGroup = groups.find((g) => g.id === 'elevenlabs')
    assert.strictEqual(elGroup, undefined)

    const oaiGroup = groups.find((g) => g.id === 'openai')
    assert.ok(oaiGroup)
    assert.ok(oaiGroup.models.some((m) => m.id.includes('gpt')))
    assert.strictEqual(oaiGroup.models.some((m) => m.id === 'dall-e-3'), false)
    assert.strictEqual(oaiGroup.models.some((m) => m.id === 'tts-1'), false)
    assert.strictEqual(oaiGroup.models.some((m) => m.id === 'whisper-1'), false)
    assert.strictEqual(oaiGroup.models.some((m) => m.id === 'text-embedding-3-small'), false)
  })

  it('includes connected compatible nodes with prefix values from aliases', () => {
    const connections: ProviderConnection[] = [
      {
        id: 'conn-node-1',
        provider: 'openai-compatible-chat-abc',
        authType: 'apikey',
        name: 'My Node',
        email: null,
        priority: 1,
        isActive: 1,
        data: '{}',
        createdAt: '',
        updatedAt: '',
        providerSpecificData: { prefix: 'Id' },
      },
    ]
    const nodes = [
      {
        id: 'openai-compatible-chat-abc',
        name: 'Custom OpenAI Node',
        type: 'openai-compatible',
        prefix: 'Id',
      } as unknown as ProviderNode,
    ]

    const groups = resolveModelPickerGroups(connections, nodes, {
      modelAliases: { 'my-alias': 'openai-compatible-chat-abc/gpt-4o' },
      customModels: [
        { providerAlias: 'openai-compatible-chat-abc', id: 'gpt-4o-mini', name: 'gpt-4o-mini', type: 'llm' },
      ],
    })
    const customGroup = groups.find((g) => g.id === 'openai-compatible-chat-abc')
    assert.ok(customGroup)
    assert.strictEqual(customGroup.name, 'Custom OpenAI Node')
    assert.strictEqual(customGroup.models.length, 2)
    assert.strictEqual(customGroup.models[0].value, 'Id/gpt-4o')
    assert.strictEqual(customGroup.models[0].caps.vision, true)
  })

  it('hides unconnected compatible nodes and filters webSearch combos', () => {
    const nodes = [
      {
        id: 'openai-compatible-chat-xyz',
        name: 'Orphan Node',
        type: 'openai-compatible',
        prefix: 'Or',
      } as unknown as ProviderNode,
    ]
    const groups = resolveModelPickerGroups([], nodes, {
      modelAliases: { 'x-alias': 'openai-compatible-chat-xyz/gpt-4o' },
    })
    assert.strictEqual(groups.find((g) => g.id === 'openai-compatible-chat-xyz'), undefined)
    const combos: Combo[] = [
      {
        id: 'c-web',
        name: 'search-combo',
        kind: 'webSearch',
        models: '[]',
        strategy: 'fallback',
        createdAt: '',
        updatedAt: '',
      },
      {
        id: 'c-llm',
        name: 'combo-wombo',
        kind: null,
        models: '[]',
        strategy: 'fallback',
        createdAt: '',
        updatedAt: '',
      },
    ]
    const filtered = resolveFilteredCombos(combos, undefined, '', 'combo')
    assert.strictEqual(filtered.length, 1)
    assert.strictEqual(filtered[0].name, 'combo-wombo')
  })

  // Deliberate NOX9GO divergence from upstream (ModelSelectModal.js 6a1573eb),
  // requested by the operator: the picker only lists providers with an ACTIVE
  // connection. A compatible node without one stays hidden even when it has
  // registered custom models — the picker is a routing surface, not a data
  // browser.
  it('hides an unconnected compatible node even when it has custom models', () => {
    const nodes = [
      {
        id: 'openai-compatible-local',
        name: 'Local Relay',
        type: 'openai-compatible',
        prefix: 'Or',
      } as unknown as ProviderNode,
    ]
    const groups = resolveModelPickerGroups([], nodes, {
      customModels: [
        { providerAlias: 'openai-compatible-local', id: 'qwen3-coder', name: 'Qwen3 Coder', type: 'llm' },
      ] as never,
    })
    const group = groups.find((g) => g.id === 'openai-compatible-local')
    assert.strictEqual(group, undefined)
  })

  it('still hides an unconnected node with no models of its own', () => {
    const nodes = [
      {
        id: 'openai-compatible-empty',
        name: 'Empty Relay',
        type: 'openai-compatible',
      } as unknown as ProviderNode,
    ]
    const groups = resolveModelPickerGroups([], nodes, {})
    assert.strictEqual(groups.find((g) => g.id === 'openai-compatible-empty'), undefined)
  })

  it('filters combos correctly', () => {
    const combos: Combo[] = [
      {
        id: 'c1',
        name: 'combo-fast',
        kind: null,
        models: JSON.stringify(['ag/gemini-flash']),
        strategy: 'fallback',
        createdAt: '',
        updatedAt: '',
      },
      {
        id: 'c2',
        name: 'combo-smart',
        kind: null,
        models: JSON.stringify(['ag/gemini-pro']),
        strategy: 'fallback',
        createdAt: '',
        updatedAt: '',
      },
    ]

    assert.strictEqual(resolveFilteredCombos(combos, undefined, '', 'combo').length, 2)

    const withoutSelf = resolveFilteredCombos(combos, 'combo-fast', '', 'combo')
    assert.strictEqual(withoutSelf.length, 1)
    assert.strictEqual(withoutSelf[0].name, 'combo-smart')

    const searched = resolveFilteredCombos(combos, undefined, 'smart', 'combo')
    assert.strictEqual(searched.length, 1)
    assert.strictEqual(searched[0].name, 'combo-smart')

    assert.strictEqual(resolveFilteredCombos(combos, undefined, '', 'vision').length, 0)
    assert.strictEqual(resolveFilteredCombos(combos, undefined, '', 'audio').length, 0)
  })

  it('filters groups by search query and capability', () => {
    const groups = resolveModelPickerGroups(
      [
        {
          id: 'conn-1',
          provider: 'antigravity',
          authType: 'oauth',
          name: 'AG',
          email: null,
          priority: 1,
          isActive: 1,
          data: '{}',
          createdAt: '',
          updatedAt: '',
        },
      ],
      []
    )

    const flashGroups = resolveFilteredGroups(groups, 'flash', 'combo')
    assert.ok(flashGroups.length > 0)
    assert.strictEqual(
      flashGroups.every((g) =>
        g.models.every((m) => m.name.toLowerCase().includes('flash') || m.id.toLowerCase().includes('flash'))
      ),
      true
    )

    const visionGroups = resolveFilteredGroups(groups, '', 'vision')
    assert.strictEqual(
      visionGroups.every((g) => g.models.every((m) => m.caps.vision === true)),
      true
    )
  })

  it('marks a model the gateway saw refuse with a 410, and leaves the rest unmarked', () => {
    const groups = resolveModelPickerGroups(
      [
        {
          id: 'conn-ds',
          provider: 'deepseek',
          authType: 'apikey',
          name: null,
          email: null,
          priority: null,
          isActive: 1,
          data: '{}',
          createdAt: '',
          updatedAt: '',
        },
      ],
      [],
      {
        deprecations: {
          // The backend keys every deprecation by a lowercased
          // "<provider>/<model>" (providers.DeprecationKey), so the picker
          // looks that up directly rather than re-normalizing per row.
          'deepseek/deepseek-chat': { status: 'gone', successor: 'deepseek-v4' },
        },
      }
    )

    const dsGroup = groups.find((g) => g.id === 'deepseek')
    assert.ok(dsGroup)
    const chat = dsGroup.models.find((m) => m.id === 'deepseek-chat')
    assert.ok(chat, 'deepseek-chat must still be listed, not hidden')
    assert.strictEqual(chat.deprecated, true)
    assert.strictEqual(
      dsGroup.models.filter((m) => m.id !== 'deepseek-chat').every((m) => !m.deprecated),
      true
    )
  })

  it('filters groups with negation: !gpt excludes gpt models under OpenAI group even when group matches', () => {
    const connections: ProviderConnection[] = [
      {
        id: 'conn-oai',
        provider: 'openai',
        authType: 'apikey',
        name: 'OpenAI',
        email: null,
        priority: 1,
        isActive: 1,
        data: '{}',
        createdAt: '',
        updatedAt: '',
      },
    ]
    const groups = resolveModelPickerGroups(connections, [])
    const oaiGroup = groups.find((g) => g.id === 'openai')
    assert.ok(oaiGroup)
    assert.ok(oaiGroup.models.some((m) => m.id.includes('gpt')))
    assert.ok(oaiGroup.models.some((m) => m.id.includes('o1')))

    // Search !gpt: OpenAI group matches "!gpt" (doesn't contain gpt), but all gpt models should be excluded!
    const filtered = resolveFilteredGroups(groups, '!gpt', 'combo')
    const filteredOai = filtered.find((g) => g.id === 'openai')
    assert.ok(filteredOai, 'OpenAI group should remain because o1 models exist')
    assert.strictEqual(
      filteredOai.models.some((m) => m.id.includes('gpt')),
      false,
      'gpt models must be excluded'
    )
    assert.ok(
      filteredOai.models.some((m) => m.id.includes('o1')),
      'o1 models must remain'
    )
  })

  it('filters combos with negation: !haiku excludes combo-haiku', () => {
    const combos: Combo[] = [
      {
        id: 'c1',
        name: 'combo-sonnet',
        kind: null,
        models: JSON.stringify(['anthropic/claude-3-5-sonnet']),
        strategy: 'fallback',
        createdAt: '',
        updatedAt: '',
      },
      {
        id: 'c2',
        name: 'combo-haiku',
        kind: null,
        models: JSON.stringify(['anthropic/claude-3-5-haiku']),
        strategy: 'fallback',
        createdAt: '',
        updatedAt: '',
      },
    ]

    const filtered = resolveFilteredCombos(combos, undefined, '!haiku', 'combo')
    assert.strictEqual(filtered.length, 1)
    assert.strictEqual(filtered[0].name, 'combo-sonnet')
  })
})
