<template>
  <!-- Messenger-style bot list: each bot is a conversation row (avatar, name,
       last message preview, relative time), most recently active first. -->
  <div class="flex h-full min-h-0 min-w-0 flex-col overflow-hidden">
    <div class="min-h-0 flex-1 overflow-y-auto px-2 pb-6 pt-1">
      <div
        v-for="row in rows"
        :key="row.bot.id"
        role="button"
        tabindex="0"
        class="group flex w-full items-center gap-3 rounded-xl px-2.5 py-2 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        :class="[
          row.bot.id === currentBotId
            ? 'bg-sidebar-accent'
            : rowHoverClass,
          row.disabled ? 'cursor-default opacity-50' : 'cursor-pointer',
        ]"
        :aria-current="row.bot.id === currentBotId ? 'true' : undefined"
        @click="openBot(row)"
        @keydown.enter.prevent="openBot(row)"
        @keydown.space.prevent="openBot(row)"
      >
        <span class="relative shrink-0">
          <Avatar class="size-10">
            <AvatarImage
              v-if="row.bot.avatar_url"
              :src="row.bot.avatar_url"
              :alt="row.label"
            />
            <AvatarFallback class="text-xs">
              {{ row.initials }}
            </AvatarFallback>
          </Avatar>
          <span
            v-if="row.pending"
            class="absolute -bottom-0.5 -right-0.5 flex size-4 items-center justify-center rounded-full bg-sidebar"
          >
            <Spinner class="size-3" />
          </span>
        </span>
        <span class="flex min-w-0 flex-1 flex-col">
          <span class="flex min-w-0 items-baseline gap-2">
            <span class="min-w-0 flex-1 truncate text-control font-semibold text-foreground">
              {{ row.label }}
            </span>
            <span
              v-if="row.timeLabel"
              class="shrink-0 text-xs text-muted-foreground"
            >
              {{ row.timeLabel }}
            </span>
          </span>
          <span class="truncate text-xs text-muted-foreground">
            {{ row.preview }}
          </span>
        </span>
      </div>

      <div
        v-if="isLoading && rows.length === 0"
        class="flex justify-center py-6"
      >
        <Spinner class="size-4" />
      </div>
      <div
        v-else-if="rows.length === 0"
        class="px-3 py-6 text-center text-xs text-muted-foreground"
      >
        {{ filter ? t('messenger.noMatches') : t('messenger.emptyBots') }}
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useQuery } from '@pinia/colada'
import { getBotsActivityQuery, getBotsQuery } from '@memohai/sdk/colada'
import type { BotsBot, BotsBotActivity } from '@memohai/sdk'
import { Avatar, AvatarFallback, AvatarImage, Spinner } from '@felinic/ui'
import { useChatStore } from '@/store/chat-list'
import { useWorkspaceTabsStore } from '@/store/workspace-tabs'
import { usePinnedBots } from '@/composables/usePinnedBots'
import { botInitials, plainPreview } from '@/utils/bot-initials'
import { formatConversationTime } from '@/utils/conversation-time'

// Same sidebar hover token the Explorer rows use (file-manager/tree-row.ts).
const rowHoverClass = 'hover:bg-[color:var(--sidebar-hover)]' /* ui-allow-style: messenger bot rows have no library owner; shared sidebar hover token */

const props = defineProps<{
  /** Case-insensitive filter on display name / slug. */
  filter?: string
}>()

interface ConversationRow {
  bot: BotsBot
  label: string
  initials: string
  preview: string
  timeLabel: string
  sortKey: number
  pending: boolean
  disabled: boolean
  sessionId: string
}

const { t, locale } = useI18n()
const router = useRouter()
const chatStore = useChatStore()
const workspaceTabs = useWorkspaceTabsStore()
const { currentBotId, sessions } = storeToRefs(chatStore)
const { isPinned } = usePinnedBots()

const { data: botData, isLoading, refetch: refetchBots } = useQuery(getBotsQuery())
// The activity endpoint is a fork addition; an older backend 404s here and the
// list degrades to names without previews.
const { data: activityData, refetch: refetchActivity } = useQuery(getBotsActivityQuery())

const activityByBot = computed(() => {
  const map = new Map<string, BotsBotActivity>()
  for (const item of activityData.value?.items ?? []) {
    if (item.bot_id) map.set(item.bot_id, item)
  }
  return map
})

function timestampOf(value?: string): number {
  if (!value) return 0
  const parsed = Date.parse(value)
  return Number.isNaN(parsed) ? 0 : parsed
}

const rows = computed<ConversationRow[]>(() => {
  const needle = (props.filter ?? '').trim().toLowerCase()
  const out: ConversationRow[] = []
  for (const bot of botData.value?.items ?? []) {
    const id = bot.id ?? ''
    if (!id) continue
    const label = bot.display_name || bot.name || id
    if (needle && !label.toLowerCase().includes(needle) && !(bot.name ?? '').toLowerCase().includes(needle)) {
      continue
    }
    const activity = activityByBot.value.get(id)
    const lastAt = activity?.last_activity_at || bot.updated_at || bot.created_at
    const pending = bot.status === 'creating' || bot.status === 'deleting'
    let preview = plainPreview(activity?.preview_text ?? '')
    if (preview && activity?.role === 'user') preview = t('messenger.youPrefix', { text: preview })
    if (!preview) preview = pending ? t('messenger.creating') : t('messenger.startChat')
    out.push({
      bot,
      label,
      initials: botInitials(label),
      preview,
      timeLabel: activity?.last_activity_at ? formatConversationTime(activity.last_activity_at, locale.value) : '',
      sortKey: timestampOf(lastAt),
      pending,
      disabled: bot.status === 'error' || pending,
      sessionId: activity?.session_id ?? '',
    })
  }
  return out.sort((a, b) => {
    const pinDiff = Number(isPinned(b.bot.id ?? '')) - Number(isPinned(a.bot.id ?? ''))
    return pinDiff || b.sortKey - a.sortKey
  })
})

// Sending or receiving in the current bot bumps its session list; refresh the
// previews then. Other bots refresh on focus and on the next bump.
const newestSessionAt = computed(() =>
  sessions.value.reduce((max, s) => Math.max(max, timestampOf(s.updated_at)), 0),
)
watch(newestSessionAt, (next, prev) => {
  if (next <= prev) return
  void refetchActivity()
  // The Bot Designer creates bots mid-turn; pick them up without a reload.
  const current = botData.value?.items?.find(bot => bot.id === currentBotId.value)
  if (current?.metadata?.bot_designer === true) void refetchBots()
})

async function openBot(row: ConversationRow) {
  if (row.disabled) return
  const id = row.bot.id ?? ''
  workspaceTabs.closeMobileNav()
  if (id !== currentBotId.value) {
    await chatStore.selectBot(id)
    await router.push({ name: 'bot', params: { botName: row.bot.name ?? id } })
  }
  // A conversation row always lands on its latest thread, including a re-click
  // on the current bot after wandering off to a draft.
  if (row.sessionId) workspaceTabs.openSessionChat({ sessionId: row.sessionId })
}
</script>
