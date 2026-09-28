<template>
  <FormDialogShell
    v-model:open="open"
    :title="t('messenger.quickCreate.title')"
    :description="t('messenger.quickCreate.description')"
    :cancel-text="t('common.cancel')"
    :submit-text="t('messenger.quickCreate.submit')"
    :submit-disabled="!canSubmit"
    :loading="creating"
    @submit="handleCreate"
  >
    <template #body>
      <FormStack class="mt-4">
        <FieldStack
          :label="t('messenger.quickCreate.name')"
          for="quick-bot-name"
        >
          <Input
            id="quick-bot-name"
            v-model="displayName"
            :disabled="creating"
            :placeholder="t('messenger.quickCreate.namePlaceholder')"
          />
        </FieldStack>
        <FieldStack :label="t('bots.settings.chatModel')">
          <ModelSelect
            v-model="chatModelId"
            :models="models"
            :providers="providers"
            model-type="chat"
            :placeholder="t('common.none')"
          />
        </FieldStack>
        <div
          v-if="creating"
          class="flex items-center gap-2 text-xs text-muted-foreground"
        >
          <Spinner class="size-3.5" />
          {{ t('messenger.quickCreate.progress', { percent }) }}
        </div>
        <p
          v-else-if="errorMessage"
          class="text-xs text-destructive"
        >
          {{ errorMessage }}
        </p>
      </FormStack>
    </template>
  </FormDialogShell>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import { useQuery, useQueryCache } from '@pinia/colada'
import { getMemoryProviders, getModels, getProviders } from '@memohai/sdk'
import { getBotsActivityQueryKey, getBotsQueryKey } from '@memohai/sdk/colada'
import { FieldStack, FormDialogShell, FormStack, Input, Spinner } from '@felinic/ui'
import ModelSelect from '@/pages/bots/components/model-select.vue'
import { useBotCreateProgressStore } from '@/store/bot-create-progress'
import { useChatStore } from '@/store/chat-list'

const open = defineModel<boolean>('open', { default: false })

const { t } = useI18n()
const router = useRouter()
const queryCache = useQueryCache()
const chatStore = useChatStore()
const createStore = useBotCreateProgressStore()
const { percent, setupError } = storeToRefs(createStore)

const displayName = ref('')
const chatModelId = ref('')
const creating = ref(false)
const errorMessage = ref('')

const { data: modelData } = useQuery({
  key: ['models'],
  query: async () => (await getModels({ throwOnError: true })).data,
})
const { data: providerData } = useQuery({
  key: ['providers'],
  query: async () => (await getProviders({ throwOnError: true })).data,
})
const { data: memoryProviderData } = useQuery({
  key: ['memory-providers'],
  query: async () => (await getMemoryProviders({ throwOnError: true })).data,
})
const models = computed(() => modelData.value ?? [])
const providers = computed(() => providerData.value ?? [])

// Default to the first enabled chat model so a bot made here can answer at once.
watch(models, (list) => {
  if (chatModelId.value) return
  const first = list.find(model => model.type === 'chat' && model.enable !== false)
  if (first?.id) chatModelId.value = first.id
}, { immediate: true })

watch(open, (isOpen) => {
  if (isOpen && !creating.value) {
    displayName.value = ''
    errorMessage.value = ''
  }
})

const canSubmit = computed(() => displayName.value.trim().length > 0 && !creating.value)

async function handleCreate() {
  if (!canSubmit.value) return
  creating.value = true
  errorMessage.value = ''
  const name = displayName.value.trim()
  const memoryProvider = (memoryProviderData.value ?? []).find(p => p.provider === 'builtin')
  try {
    createStore.reset()
    await createStore.start(
      // The server derives a unique slug from the display name.
      { display_name: name, is_active: true, wait_for_ready: true },
      {
        display: { display_name: name },
        settings: {
          chat_model_id: chatModelId.value || undefined,
          memory_provider_id: memoryProvider?.id || undefined,
        },
      },
    )
    const bot = createStore.bot
    if (!bot?.id) {
      errorMessage.value = setupError.value || t('messenger.quickCreate.failed')
      return
    }
    void queryCache.invalidateQueries({ key: getBotsQueryKey() })
    void queryCache.invalidateQueries({ key: getBotsActivityQueryKey() })
    await chatStore.refreshBots()
    await chatStore.selectBot(bot.id)
    await router.push({ name: 'bot', params: { botName: bot.name ?? bot.id } })
    createStore.reset()
    open.value = false
  } finally {
    creating.value = false
  }
}
</script>
