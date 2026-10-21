<script setup lang="ts">
import {
  Ban,
  Check,
  CircleCheck,
  Download,
  Equal,
  KeyRound,
  KeySquare,
  LoaderCircle,
  OctagonX,
  Pencil,
  Plus,
  RefreshCw,
  RotateCcw,
  ShieldCheck,
  ShieldOff,
  Trash2,
  TriangleAlert,
  Upload,
} from '@lucide/vue'
import { computed, ref, watch, watchEffect, type Component } from 'vue'

import {
  useChangePassword,
  useCreateToken,
  useDeleteToken,
  useDisableTwoFactor,
  useEnableTwoFactor,
  useSession,
  useTokens,
  useTwoFactorSecret,
  useUpdateAccount,
} from '../api/account'
import { saveFile, useCertSettings, useSaveCertSettings, type CertSettingsInput, type KeyType } from '../api/certs'
import { useExportConfig, useImportConfig, type ConfigItem, type ConfigReport } from '../api/config'
import { useApiError } from '../api/errors'
import { useManifest } from '../api/manifest'
import {
  lowDisk,
  useBackup,
  useHealth,
  useSetHeartbeat,
  useReleaseCheck,
  useRestore,
  useSecretKey,
  useSetAutoBackup,
  useSetTimeZone,
  useSetUpdateCheck,
  useSystem,
  waitForRestart,
  type Health,
} from '../api/system'
import { keyText } from '../certs/life'
import CopyButton from '../components/CopyButton.vue'
import JecAlert from '../components/JecAlert.vue'
import JecButton from '../components/JecButton.vue'
import JecDialog from '../components/JecDialog.vue'
import JecField from '../components/JecField.vue'
import JecInput from '../components/JecInput.vue'
import JobsDialog from '../components/JobsDialog.vue'
import OffsiteSettings from '../components/OffsiteSettings.vue'
import OidcSettings from '../components/OidcSettings.vue'
import RecoveryCodes from '../components/RecoveryCodes.vue'
import SessionsSettings from '../components/SessionsSettings.vue'
import PageHeader from '../components/PageHeader.vue'
import { locales, setLocale, useT, type Locale, type MessageKey, type Params } from '../i18n'
import { useIntl } from '../i18n/intl'
import { accent, accents, setAccent, setStyle, setTheme, style, styles, theme, themes, type Accent, type Style, type Theme } from '../theme'
import { fromMore } from '../shell'
import { zones } from '../zone'

// The settings (docs/design/boards/Settings and SettingsMobile), each saved
// as soon as it changes. Those of the account, the panel's address and the
// backups come later.
const { t, locale } = useT()
const intl = useIntl()
const manifest = useManifest()

// The theme of this browser, with its color and style.
const accentLabels: Record<Accent, MessageKey> = {
  green: 'kernel.settings.accent_green',
  blue: 'kernel.settings.accent_blue',
  teal: 'kernel.settings.accent_teal',
  purple: 'kernel.settings.accent_purple',
  pink: 'kernel.settings.accent_pink',
}
const styleLabels: Record<Style, MessageKey> = { plain: 'kernel.settings.style_plain', blocks: 'kernel.settings.style_blocks' }
const themeLabels: Record<Theme, MessageKey> = {
  system: 'kernel.settings.theme_system',
  light: 'kernel.settings.theme_light',
  dark: 'kernel.settings.theme_dark',
}

// Whether a newer release is out, when someone asks.
const release = useReleaseCheck()
const setUpdateCheck = useSetUpdateCheck()
const releaseError = useApiError(computed(() => release.error.value))
// A build of its own, as dev, has no version to compare.
const versioned = computed(() => /^\d/.test(release.data.value?.current ?? ''))
// How to move to it: pull the image, or have jec replace itself.
const upgradeCommand = computed(() => (system.data.value?.image ? 'docker compose pull && docker compose up -d' : 'sudo jec upgrade -restart'))
// Single sign-on, while its module is on.
const oidcOn = computed(() => manifest.data.value?.modules.some((m) => m.id === 'oidc' && m.enabled) ?? false)
const offsiteOn = computed(() => manifest.data.value?.modules.some((m) => m.id === 'offsite' && m.enabled) ?? false)

// The defaults of new certificates and the time renewals start, of the
// certs module.
const settings = useCertSettings()
const save = useSaveCertSettings()
const loadError = useApiError(computed(() => settings.error.value))
const saveError = useApiError(computed(() => save.error.value))
const form = ref<CertSettingsInput>()
watchEffect(() => {
  const st = settings.data.value
  if (st && !form.value) form.value = { renewBeforeDays: st.renewBeforeDays, keyType: st.keyType, checkTime: st.checkTime, retries: st.retries }
})
const keyTypes: KeyType[] = ['rsa-2048', 'ecdsa-p256', 'ecdsa-p384', 'rsa-3072', 'rsa-4096']
const keyLabel = (k: string) => {
  const text = keyText(k)
  return t(text.key, text.params)
}
const saved = computed(() => save.isSuccess.value && !save.isPending.value)

async function store() {
  if (!form.value) return
  try {
    await save.mutateAsync({ ...form.value })
  } catch {
    // shown by the fields
  }
}

// The account: its password, changed in a dialog once the current one is
// confirmed, which signs it out everywhere else.
const session = useSession()
const change = useChangePassword()
const changeError = useApiError(computed(() => change.error.value))
const changing = ref(false)
const changed = ref(false)
const pw = ref({ current: '', next: '', again: '' })
const mismatch = ref(false)
watch(changing, (open) => {
  if (!open) return
  pw.value = { current: '', next: '', again: '' }
  mismatch.value = false
  change.reset()
})

async function submitPassword() {
  mismatch.value = pw.value.next !== pw.value.again
  if (mismatch.value) return
  try {
    await change.mutateAsync({ password: pw.value.current, newPassword: pw.value.next })
  } catch {
    return // shown by the fields
  }
  changing.value = false
  changed.value = true
}

// Two-factor authentication: turned on in a dialog of two steps, the
// password and then a code of the new secret, which signs the account out
// everywhere else; turned off once the password is confirmed. And how many
// days a sign-in lasts, saved as soon as it changes.
const secret = useTwoFactorSecret()
const enable = useEnableTwoFactor()
const disable = useDisableTwoFactor()
const updateAccount = useUpdateAccount()
const secretError = useApiError(computed(() => secret.error.value))
const enableError = useApiError(computed(() => enable.error.value))
const disableError = useApiError(computed(() => disable.error.value))
const accountError = useApiError(computed(() => updateAccount.error.value))
const turningOn = ref(false)
const turningOff = ref(false)
const twoFactorDone = ref<'on' | 'off'>()
// The recovery codes made as it turned on, shown once.
const newCodes = ref<string[]>()
const tfPassword = ref('')
const tfCode = ref('')
const resetCommand = 'jec reset-password'
watch(turningOn, (open) => {
  if (!open) return
  tfPassword.value = ''
  tfCode.value = ''
  secret.reset()
  enable.reset()
})
watch(turningOff, (open) => {
  if (!open) return
  tfPassword.value = ''
  disable.reset()
})
// The key in groups of four, as people type it.
const groupedKey = computed(() => secret.data.value?.secret.match(/.{1,4}/g)?.join(' ') ?? '')

async function submitTurnOn() {
  try {
    if (!secret.data.value) {
      await secret.mutateAsync({ password: tfPassword.value })
      return // the QR code shows next
    }
    newCodes.value = (await enable.mutateAsync({ code: tfCode.value })).recoveryCodes
  } catch {
    // A secret that waited too long goes: the dialog starts over.
    if (enableError.error.value?.code === 'kernel.error.two_factor_expired') secret.reset()
    return // shown by the fields
  }
  turningOn.value = false
  twoFactorDone.value = 'on'
}

async function submitTurnOff() {
  try {
    await disable.mutateAsync({ password: tfPassword.value })
  } catch {
    return // shown by the field
  }
  turningOff.value = false
  twoFactorDone.value = 'off'
}

const sessionDays = [1, 7, 30] as const
async function pickSessionDays(e: Event) {
  try {
    await updateAccount.mutateAsync({ sessionDays: Number((e.target as HTMLSelectElement).value) as (typeof sessionDays)[number] })
  } catch {
    // shown above the settings of the account
  }
}

// API tokens: listed and made in a dialog, a new one's value shown once,
// and deleted at a second click.
const tokensOpen = ref(false)
const tokens = useTokens(computed(() => !!session.data.value))
const createToken = useCreateToken()
const deleteToken = useDeleteToken()
const tokenError = useApiError(computed(() => createToken.error.value))
const deleteTokenError = useApiError(computed(() => deleteToken.error.value))
const emptyToken = () => ({ name: '', scope: 'read' as 'read' | 'write', days: 0, password: '' })
// How long a new token works: for ever, or a number of days.
const tokenDays = [0, 30, 90, 365]
const tokenEnded = (at?: string) => !!at && Date.parse(at) <= Date.now()
const tokenForm = ref(emptyToken())
const madeToken = ref<{ name: string; token: string }>()
const confirmDelete = ref<number>()
const scopeText = { read: 'kernel.tokens.scope_read', write: 'kernel.tokens.scope_write' } as const
watch(tokensOpen, (open) => {
  if (!open) return
  tokenForm.value = emptyToken()
  madeToken.value = undefined
  confirmDelete.value = undefined
  createToken.reset()
  deleteToken.reset()
})

async function submitToken() {
  let made
  try {
    made = await createToken.mutateAsync({ ...tokenForm.value, name: tokenForm.value.name.trim() })
  } catch {
    return // shown by the fields
  }
  madeToken.value = { name: made.detail.name, token: made.token }
  tokenForm.value = emptyToken()
}

async function removeToken(id: number) {
  if (confirmDelete.value !== id) {
    confirmDelete.value = id
    return
  }
  try {
    await deleteToken.mutateAsync(id)
  } catch {
    return // shown above the list
  }
  confirmDelete.value = undefined
}

// How JEC is reached and where its data is, set outside the panel; and a
// copy of the database, once the password is confirmed.
const system = useSystem()

// How the background jobs went last, and the space left for the data: a
// failure nothing else shows; the jobs in a dialog.
const health = useHealth()
type Job = Health['jobs'][number]
const failedJobs = computed(() => (health.data.value?.jobs ?? []).filter((j) => j.error))
const jobsOpen = ref(false)
const moduleName = (j: Job) => {
  if (j.module === 'kernel') return t('kernel.health.kernel')
  const m = manifest.data.value?.modules.find((x) => x.id === j.module)
  return m ? t(m.name as MessageKey) : j.module
}
const diskLow = computed(() => lowDisk(health.data.value?.disk))
const diskText = computed(() => {
  const disk = health.data.value?.disk
  return disk ? t('kernel.health.disk_free', { free: intl.bytes(disk.free), total: intl.bytes(disk.total) }) : ''
})

// The time zone of the whole panel, saved as soon as it changes: the one
// chosen, or the server's.
// The daily backups JEC makes by itself, how many it keeps.
const keeps = [0, 3, 7, 14, 30] as const
const setAutoBackup = useSetAutoBackup()
const autoBackupError = useApiError(computed(() => setAutoBackup.error.value))
function pickKeep(e: Event) {
  setAutoBackup.mutate(Number((e.target as HTMLSelectElement).value))
}

// The heartbeat: saved when the field is left, if changed.
const setHeartbeat = useSetHeartbeat()
const heartbeatError = useApiError(computed(() => setHeartbeat.error.value))
const heartbeat = ref('')
watch(
  () => system.data.value?.heartbeat.url,
  (url) => (heartbeat.value = url ?? ''),
  { immediate: true },
)
function saveHeartbeat() {
  const url = heartbeat.value.trim()
  if (url !== (system.data.value?.heartbeat.url ?? '')) setHeartbeat.mutate(url)
}

const setTimeZone = useSetTimeZone()
const zoneError = useApiError(computed(() => setTimeZone.error.value))
const zoneChoices = computed(() => zones(system.data.value?.timeZone ?? ''))
const zoneSaved = computed(() => setTimeZone.isSuccess.value && !setTimeZone.isPending.value)
async function pickZone(e: Event) {
  try {
    await setTimeZone.mutateAsync((e.target as HTMLSelectElement).value)
  } catch {
    // shown by the field
  }
}

const backup = useBackup()
const backupError = useApiError(computed(() => backup.error.value))
const backingUp = ref(false)
const backupPassword = ref('')
watch(backingUp, (open) => {
  if (!open) return
  backupPassword.value = ''
  backup.reset()
})

// The master key, which backups lack: told once the password is right, to
// keep apart, as in a password manager.
const secretKey = useSecretKey()
const secretKeyError = useApiError(computed(() => secretKey.error.value))
const showingKey = ref(false)
const keyPassword = ref('')
watch(showingKey, (open) => {
  if (!open) return
  keyPassword.value = ''
  secretKey.reset()
})
async function submitKey() {
  try {
    await secretKey.mutateAsync(keyPassword.value)
  } catch {
    // shown by the field
  }
}

async function submitBackup() {
  let file
  try {
    file = await backup.mutateAsync(backupPassword.value)
  } catch {
    return // shown by the field
  }
  saveFile(file.blob, file.name)
  backingUp.value = false
}

// A backup restored: sent with the password, after which JEC stops and
// starts again with it; the page waits for it and loads anew.
const restoring = ref(false)
const restore = useRestore()
const restoreError = useApiError(computed(() => restore.error.value))
const restoreFile = ref<File>()
const restorePassword = ref('')
const restoreState = ref<'form' | 'waiting' | 'slow'>('form')
watch(restoring, (open) => {
  if (!open || restoreState.value !== 'form') return
  restoreFile.value = undefined
  restorePassword.value = ''
  restore.reset()
})

function pickBackup(e: Event) {
  restoreFile.value = (e.target as HTMLInputElement).files?.[0]
}

async function submitRestore() {
  if (!restoreFile.value) return
  try {
    await restore.mutateAsync({ file: restoreFile.value, password: restorePassword.value })
  } catch {
    return // shown above the fields
  }
  restoreState.value = 'waiting'
  if (await waitForRestart()) {
    location.reload()
    return
  }
  restoreState.value = 'slow'
}

// The configuration as a YAML file (PLAN.md 11): exported, with the
// secrets once the password is confirmed; or imported once a dry run of
// the file showed what it would change.
const exportConfig = useExportConfig()
const exportError = useApiError(computed(() => exportConfig.error.value))
const exportingSecrets = ref(false)
const exportPassword = ref('')
watch(exportingSecrets, (open) => {
  if (!open) return
  exportPassword.value = ''
  exportConfig.reset()
})

async function downloadConfig(password?: string): Promise<boolean> {
  let file
  try {
    file = await exportConfig.mutateAsync(password)
  } catch {
    return false // shown in the row, or by the field
  }
  saveFile(file.blob, file.name)
  return true
}

async function submitExportSecrets() {
  if (await downloadConfig(exportPassword.value)) exportingSecrets.value = false
}

const importing = ref(false)
const importConfig = useImportConfig()
const importError = useApiError(computed(() => importConfig.error.value))
const configFile = ref<{ name: string; text: string }>()
const report = ref<ConfigReport>()
const imported = ref(false)
watch(importing, (open) => {
  if (!open) return
  configFile.value = undefined
  report.value = undefined
  imported.value = false
  importConfig.reset()
})

async function pickConfig(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = '' // so that the same file, once fixed, can be chosen again
  if (!file) return
  configFile.value = { name: file.name, text: await file.text() }
  report.value = undefined
  try {
    report.value = await importConfig.mutateAsync({ file: configFile.value.text, apply: false })
  } catch {
    // shown above the report
  }
}

const changes = computed(() => report.value?.sections.some((s) => s.items.some((i) => i.action === 'add' || i.action === 'change')) ?? false)

async function applyConfig() {
  if (!configFile.value) return
  try {
    report.value = await importConfig.mutateAsync({ file: configFile.value.text, apply: true })
  } catch {
    return // shown above the report
  }
  if (report.value.applied) {
    importing.value = false
    imported.value = true
    // The form keeps what it showed: it shows the defaults as imported.
    const st = (await settings.refetch()).data
    if (st) form.value = { renewBeforeDays: st.renewBeforeDays, keyType: st.keyType, checkTime: st.checkTime }
  }
}

// How each action looks: an icon and a word, never color alone.
const actionLooks: Record<ConfigItem['action'], { icon: Component; cls: string; text: MessageKey }> = {
  add: { icon: Plus, cls: 'bg-ok-soft text-ok-ink', text: 'kernel.config.action.add' },
  change: { icon: Pencil, cls: 'bg-info-soft text-info-ink', text: 'kernel.config.action.change' },
  keep: { icon: Equal, cls: 'bg-surface-sunken text-ink-muted', text: 'kernel.config.action.keep' },
  skip: { icon: Ban, cls: 'bg-warn-soft text-warn-ink', text: 'kernel.config.action.skip' },
}
const problemText = (p: NonNullable<ConfigItem['problems']>[number]) => t(p.key as MessageKey, (p.params ?? {}) as Params)
// Entries that stay as they are take a line together; the others, a box
// each, with their problems.
type ReportSection = ConfigReport['sections'][number]
const unchanged = (i: ConfigItem) => i.action === 'keep' && !i.problems?.length
const listed = (s: ReportSection) => s.items.filter((i) => !unchanged(i))
const kept = (s: ReportSection) => s.items.filter(unchanged)

function pickLocale(e: Event) {
  void setLocale((e.target as HTMLSelectElement).value as Locale)
}

// The repository's name is data, not words to translate, as are the
// variables that set how the panel runs.
const repository = { name: 'XMZO/JustEnoughCerts', url: 'https://github.com/XMZO/JustEnoughCerts' }
// The databases, by their own names.
const databases = { sqlite: 'SQLite', postgres: 'PostgreSQL' } as const
const variables = { database: 'JEC_DATABASE_URL', baseURL: 'JEC_BASE_URL', listen: 'JEC_LISTEN', trustedProxies: 'JEC_TRUSTED_PROXIES', allowedClients: 'JEC_ALLOWED_CLIENTS', tls: 'JEC_TLS_CERT', hsts: 'JEC_HSTS' }

// The cards in columns, packed from the top; the last of each column
// stretches, so that the columns end on one line. From 1280px three
// columns hold them all in one screen, as the user asked.
const panel = 'flex flex-col rounded-lg border border-line bg-surface-raised px-3 py-2 sm:px-3.5 sm:py-2'
const heading = 'm-0 text-heading font-semibold'
const row = 'flex min-h-10 items-center gap-x-3 gap-y-1 border-b border-line py-1 last:border-b-0'
// The words under a label, two lines at most from 640px and one in three
// columns; the title has them all.
const help = 'block text-caption text-ink-muted sm:line-clamp-2 xl:line-clamp-1'
// Two lines in the cards of the columns with room to spare.
const help2 = 'block text-caption text-ink-muted sm:line-clamp-2'
// The read-only facts of the about and the runtime cards, a line each, cut
// short with the whole in the title where they are long.
const fact = 'm-0 min-w-0 text-right [overflow-wrap:anywhere]'
const line = 'm-0 min-w-0 truncate text-right'
// A fact that a variable sets: its title names the variable.
const variable = 'cursor-help font-semibold underline decoration-ink-muted decoration-dotted underline-offset-4'
const control = 'h-9 rounded-md border border-line-strong bg-surface-raised px-2 text-ink aria-invalid:border-danger-ink max-sm:h-11 sm:h-8 sm:text-small'
// In a dialog's form, as tall as the inputs beside it.
const field = 'h-9 rounded-md border border-line-strong bg-surface-raised px-2 text-ink aria-invalid:border-danger-ink max-sm:h-11'
</script>

<template>
  <PageHeader :title="t('kernel.settings.title')" :phone-back="fromMore">
    <template #badge>
      <span class="text-small text-ink-muted">{{ t('kernel.settings.intro') }}</span>
    </template>
  </PageHeader>

  <!--
    Three groups: how it looks and renews, the data and JEC itself, the
    account and how JEC runs. From 640px the first two side by side and the
    third under them in two columns; from 1280px three columns.
  -->
  <div class="flex flex-col gap-3 px-4 pt-3 sm:mt-3 sm:grid sm:grid-cols-2 sm:p-0 xl:grid-cols-3">
    <p class="m-0 text-ink-muted sm:hidden">{{ t('kernel.settings.intro') }}</p>

    <div class="flex flex-col gap-3">
      <section aria-labelledby="appearance" :class="panel">
        <div class="flex flex-wrap items-center justify-between gap-x-3">
          <h2 id="appearance" :class="heading">{{ t('kernel.settings.appearance') }}</h2>
          <p v-if="zoneSaved" role="status" class="m-0 inline-flex items-center gap-1 text-small text-ok-ink">
            <Check class="size-4" aria-hidden="true" />{{ t('kernel.settings.saved') }}
          </p>
        </div>
        <JecAlert v-if="zoneError.alert.value" :code="zoneError.alert.value.code" :params="zoneError.alert.value.params" />
        <div :class="row">
          <label for="language" class="min-w-0 flex-1 font-semibold">{{ t('kernel.settings.language') }}</label>
          <select id="language" :value="locale" :class="[control, 'flex-none']" @change="pickLocale">
            <option v-for="l in locales" :key="l.tag" :value="l.tag" :lang="l.tag" translate="no">{{ l.name }}</option>
          </select>
        </div>
        <div :class="[row, 'flex-wrap']">
          <span id="theme-label" class="min-w-0 flex-[1_1_4rem] font-semibold">{{ t('kernel.settings.theme') }}</span>
          <div
            role="group"
            aria-labelledby="theme-label"
            class="inline-flex flex-none gap-0.5 rounded-md border border-line bg-surface-sunken p-0.5 max-sm:flex max-sm:w-full"
          >
            <button
              v-for="th in themes"
              :key="th"
              type="button"
              :aria-pressed="theme === th ? 'true' : 'false'"
              class="h-7 cursor-pointer rounded-sm border-0 bg-transparent px-2.5 text-[13px] font-semibold whitespace-nowrap text-ink-muted hover:text-ink aria-pressed:bg-surface-raised aria-pressed:text-ink aria-pressed:shadow-[0_0_0_1px_var(--line-strong)] max-sm:h-auto max-sm:min-h-9 max-sm:min-w-0 max-sm:flex-1 max-sm:px-1.5 max-sm:py-1 max-sm:whitespace-normal"
              @click="setTheme(th)"
            >
              {{ t(themeLabels[th]) }}
            </button>
          </div>
        </div>
        <div :class="[row, 'flex-wrap']">
          <span id="accent-label" class="min-w-0 flex-[1_1_4rem] font-semibold">{{ t('kernel.settings.accent') }}</span>
          <!-- Each swatch takes its own color: data-accent sets the brand's tokens within it. -->
          <div role="group" aria-labelledby="accent-label" class="flex flex-none flex-wrap gap-1.5">
            <button
              v-for="a in accents"
              :key="a"
              type="button"
              :data-accent="a"
              :aria-pressed="accent === a ? 'true' : 'false'"
              :aria-label="t(accentLabels[a])"
              :title="t(accentLabels[a])"
              class="grid size-7 cursor-pointer place-items-center rounded-full border-2 border-surface-raised bg-brand p-0 text-on-brand shadow-[0_0_0_1px_var(--line-strong)] aria-pressed:shadow-[0_0_0_2px_var(--ink)] max-sm:size-11"
              @click="setAccent(a)"
            >
              <Check v-if="accent === a" class="size-4" aria-hidden="true" />
            </button>
          </div>
        </div>
        <div :class="[row, 'flex-wrap']">
          <span id="style-label" class="min-w-0 flex-[1_1_4rem] font-semibold">{{ t('kernel.settings.style') }}</span>
          <div
            role="group"
            aria-labelledby="style-label"
            class="inline-flex flex-none gap-0.5 rounded-md border border-line bg-surface-sunken p-0.5 max-sm:flex max-sm:w-full"
          >
            <button
              v-for="st in styles"
              :key="st"
              type="button"
              :aria-pressed="style === st ? 'true' : 'false'"
              :title="st === 'blocks' ? t('kernel.settings.style_help') : undefined"
              class="h-7 cursor-pointer rounded-sm border-0 bg-transparent px-2.5 text-[13px] font-semibold whitespace-nowrap text-ink-muted hover:text-ink aria-pressed:bg-surface-raised aria-pressed:text-ink aria-pressed:shadow-[0_0_0_1px_var(--line-strong)] max-sm:h-auto max-sm:min-h-9 max-sm:min-w-0 max-sm:flex-1 max-sm:px-1.5 max-sm:py-1 max-sm:whitespace-normal"
              @click="setStyle(st)"
            >
              {{ t(styleLabels[st]) }}
            </button>
          </div>
        </div>
        <div v-if="system.data.value" :class="row">
          <div class="min-w-0 flex-1">
            <label for="timeZone" class="block font-semibold">{{ t('kernel.settings.time_zone') }}</label>
            <span v-if="zoneError.fields.value.timeZone" id="zone-help" class="block text-caption text-danger-ink">{{ t(zoneError.fields.value.timeZone) }}</span>
          </div>
          <select
            id="timeZone"
            :value="system.data.value.timeZone ?? ''"
            :aria-describedby="zoneError.fields.value.timeZone ? 'zone-help' : undefined"
            :aria-invalid="!!zoneError.fields.value.timeZone || undefined"
            :class="[control, 'max-w-[70%] min-w-0 flex-none']"
            @change="pickZone"
          >
            <option value="">{{ t('kernel.settings.time_zone_server', { zone: system.data.value.serverTimeZone }) }}</option>
            <option v-for="z in zoneChoices" :key="z" :value="z" translate="no">{{ z }}</option>
          </select>
        </div>
        <p class="m-0 pt-1 pb-0.5 text-caption text-ink-muted">{{ t('kernel.settings.browser_only') }}</p>
      </section>

      <section aria-labelledby="renewal" :class="[panel, 'sm:flex-1']">
        <div class="flex flex-wrap items-center justify-between gap-x-3">
          <h2 id="renewal" :class="heading">{{ t('certs.defaults.title') }}</h2>
          <p v-if="saved" role="status" class="m-0 inline-flex items-center gap-1 text-small text-ok-ink">
            <Check class="size-4" aria-hidden="true" />{{ t('kernel.settings.saved') }}
          </p>
        </div>
        <JecAlert v-if="loadError.error.value" :code="loadError.error.value.code" :params="loadError.error.value.params" />
        <JecAlert v-if="saveError.alert.value" :code="saveError.alert.value.code" :params="saveError.alert.value.params" />
        <template v-if="form">
          <div :class="row">
            <div class="min-w-0 flex-1">
              <label for="renewBeforeDays" class="block font-semibold">{{ t('certs.defaults.days') }}</label>
              <span id="days-help" :class="help2" :title="t('certs.defaults.days_help')">
                {{ saveError.fields.value.renewBeforeDays ? t(saveError.fields.value.renewBeforeDays) : t('certs.defaults.days_help') }}
              </span>
            </div>
            <span class="flex flex-none items-center gap-2">
              <input
                id="renewBeforeDays"
                v-model.number="form.renewBeforeDays"
                type="number"
                inputmode="numeric"
                min="1"
                max="365"
                aria-describedby="days-help"
                :aria-invalid="!!saveError.fields.value.renewBeforeDays || undefined"
                :class="[control, 'w-16 tabular-nums']"
                @change="store"
              />
              <span>{{ t('certs.settings.days_unit') }}</span>
            </span>
          </div>
          <div :class="row">
            <div class="min-w-0 flex-1">
              <label for="checkTime" class="block font-semibold">{{ t('certs.defaults.check_time') }}</label>
              <span id="check-help" :class="help2" :title="t('certs.defaults.check_time_help', { zone: settings.data.value?.timeZone ?? '' })">
                {{
                  saveError.fields.value.checkTime
                    ? t(saveError.fields.value.checkTime)
                    : t('certs.defaults.check_time_help', { zone: settings.data.value?.timeZone ?? '' })
                }}
              </span>
            </div>
            <input
              id="checkTime"
              v-model="form.checkTime"
              type="time"
              required
              aria-describedby="check-help"
              :aria-invalid="!!saveError.fields.value.checkTime || undefined"
              :class="[control, 'w-28 flex-none tabular-nums']"
              @change="store"
            />
          </div>
          <div :class="row">
            <div class="min-w-0 flex-1">
              <label for="retries" class="block font-semibold">{{ t('certs.defaults.retries') }}</label>
              <span id="retries-help" :class="help2" :title="t('certs.defaults.retries_help')">
                {{ saveError.fields.value.retries ? t(saveError.fields.value.retries) : t('certs.defaults.retries_help') }}
              </span>
            </div>
            <span class="flex flex-none items-center gap-2">
              <input
                id="retries"
                v-model.number="form.retries"
                type="number"
                inputmode="numeric"
                min="0"
                max="20"
                aria-describedby="retries-help"
                :aria-invalid="!!saveError.fields.value.retries || undefined"
                :class="[control, 'w-16 tabular-nums']"
                @change="store"
              />
              <span>{{ t('certs.defaults.retries_unit', { count: form.retries ?? 0 }) }}</span>
            </span>
          </div>
          <div :class="row">
            <div class="min-w-0 flex-1">
              <label for="keyType" class="block font-semibold">{{ t('certs.defaults.key_type') }}</label>
              <span id="key-help" :class="help2" :title="t('certs.defaults.key_type_help')">{{ t('certs.defaults.key_type_help') }}</span>
            </div>
            <select id="keyType" v-model="form.keyType" aria-describedby="key-help" :class="[control, 'flex-none']" @change="store">
              <option v-for="k in keyTypes" :key="k" :value="k">{{ keyLabel(k) }}</option>
            </select>
          </div>
        </template>
      </section>
    </div>

    <div class="flex flex-col gap-3">
      <section v-if="system.data.value" aria-labelledby="data" :class="panel">
        <h2 id="data" :class="heading">{{ t('kernel.settings.data') }}</h2>
        <div :class="[row, 'flex-wrap']">
          <div class="min-w-0 flex-[1_1_6rem]">
            <span class="block font-semibold">{{ t('kernel.settings.backup') }}</span>
            <span :class="help2" :title="t('kernel.settings.backup_help')">{{ t('kernel.settings.backup_help') }}</span>
          </div>
          <div class="ml-auto flex max-w-full min-w-0 flex-wrap justify-end gap-1">
            <JecButton size="sm" @click="backingUp = true">
              <Download class="size-4" aria-hidden="true" />{{ t('kernel.settings.backup_download') }}
            </JecButton>
            <JecButton size="sm" @click="restoring = true">
              <RotateCcw class="size-4" aria-hidden="true" />{{ t('kernel.settings.restore') }}
            </JecButton>
          </div>
        </div>
        <div :class="row">
          <div class="min-w-0 flex-1">
            <label for="autoBackup" class="block font-semibold">{{ t('kernel.settings.auto_backup') }}</label>
            <span id="auto-backup-help" :class="help2">
              <template v-if="system.data.value.autoBackup.last">
                {{ t('kernel.settings.auto_backup_last', { count: system.data.value.autoBackup.count }) }}
                <time :datetime="system.data.value.autoBackup.last">{{ intl.dateTime(system.data.value.autoBackup.last) }}</time>
              </template>
              <span v-else :title="t('kernel.settings.auto_backup_help')">{{ t('kernel.settings.auto_backup_help') }}</span>
            </span>
            <JecAlert v-if="autoBackupError.alert.value" class="mt-2" :code="autoBackupError.alert.value.code" :params="autoBackupError.alert.value.params" />
          </div>
          <select
            id="autoBackup"
            :value="system.data.value.autoBackup.keep"
            aria-describedby="auto-backup-help"
            :class="[control, 'flex-none']"
            @change="pickKeep"
          >
            <option v-for="k in keeps" :key="k" :value="k">
              {{ k ? t('kernel.settings.auto_backup_keep', { count: k }) : t('kernel.settings.auto_backup_off') }}
            </option>
          </select>
        </div>
        <OffsiteSettings v-if="offsiteOn" :row-class="row" />
        <div :class="[row, 'flex-wrap']">
          <div class="min-w-0 flex-[1_1_8rem]">
            <label for="heartbeat" class="block font-semibold">{{ t('kernel.settings.heartbeat') }}</label>
            <span id="heartbeat-help" :class="help2">
              <template v-if="heartbeatError.fields.value.url">{{ t(heartbeatError.fields.value.url) }}</template>
              <template v-else-if="system.data.value.heartbeat.last && system.data.value.heartbeat.error">
                <span class="font-semibold text-warn-ink">{{ t('kernel.settings.heartbeat_failed') }}</span>
                <time :datetime="system.data.value.heartbeat.last">{{ intl.dateTime(system.data.value.heartbeat.last) }}</time>
                <span translate="no" :title="system.data.value.heartbeat.error">{{ system.data.value.heartbeat.error }}</span>
              </template>
              <template v-else-if="system.data.value.heartbeat.last">
                {{ t('kernel.settings.heartbeat_ok') }}
                <time :datetime="system.data.value.heartbeat.last">{{ intl.dateTime(system.data.value.heartbeat.last) }}</time>
              </template>
              <span v-else :title="t('kernel.settings.heartbeat_help')">{{ t('kernel.settings.heartbeat_help') }}</span>
            </span>
          </div>
          <input
            id="heartbeat"
            v-model="heartbeat"
            type="url"
            inputmode="url"
            autocomplete="off"
            spellcheck="false"
            aria-describedby="heartbeat-help"
            :aria-invalid="!!heartbeatError.fields.value.url || undefined"
            :class="[control, 'w-full min-w-0 flex-[1_1_9rem]']"
            @change="saveHeartbeat"
          />
        </div>
        <div :class="[row, 'flex-wrap']">
          <div class="min-w-0 flex-[1_1_8rem]">
            <span class="block font-semibold">{{ t('kernel.settings.config') }}</span>
            <span :class="help2" :title="t('kernel.settings.config_help')">{{ t('kernel.settings.config_help') }}</span>
            <span v-if="imported" role="status" class="mt-1 block text-small font-semibold text-ok-ink">{{ t('kernel.settings.config_imported') }}</span>
            <JecAlert
              v-if="exportError.alert.value && !exportingSecrets"
              class="mt-2"
              :code="exportError.alert.value.code"
              :params="exportError.alert.value.params"
            />
          </div>
          <div class="ml-auto flex max-w-full min-w-0 flex-wrap justify-end gap-1">
            <JecButton size="sm" :busy="exportConfig.isPending.value && !exportingSecrets" @click="downloadConfig()">
              <Download class="size-4" aria-hidden="true" />{{ t('kernel.settings.config_export') }}
            </JecButton>
            <JecButton size="sm" @click="exportingSecrets = true">
              <KeyRound class="size-4" aria-hidden="true" />{{ t('kernel.settings.config_export_secrets') }}
            </JecButton>
            <JecButton size="sm" @click="importing = true">
              <Upload class="size-4" aria-hidden="true" />{{ t('kernel.settings.config_import') }}
            </JecButton>
          </div>
        </div>
        <div :class="[row, 'flex-wrap']">
          <div class="min-w-0 flex-[1_1_8rem]">
            <span class="flex min-w-0 items-baseline gap-x-2">
              <span class="flex-none font-semibold">{{ t('kernel.settings.secret_key') }}</span>
              <span class="min-w-0 truncate text-caption text-ink-muted" :title="t(system.data.value.secretKey === 'env' ? 'kernel.settings.secret_key_env' : 'kernel.settings.secret_key_file')">{{
                t(system.data.value.secretKey === 'env' ? 'kernel.settings.secret_key_env' : 'kernel.settings.secret_key_file')
              }}</span>
            </span>
            <span :class="help2" :title="t('kernel.settings.secret_key_help')">{{ t('kernel.settings.secret_key_help') }}</span>
          </div>
          <JecButton size="sm" class="ml-auto flex-none" :aria-label="t('kernel.settings.secret_key_show_label')" @click="showingKey = true">
            <KeyRound class="size-4" aria-hidden="true" />{{ t('kernel.settings.secret_key_show') }}
          </JecButton>
        </div>
      </section>

      <section v-if="manifest.data.value" aria-labelledby="about" :class="[panel, 'sm:flex-1']">
        <h2 id="about" :class="heading">{{ t('kernel.settings.about') }}</h2>
        <dl class="m-0 grid grid-cols-[auto_minmax(0,1fr)] items-baseline gap-x-4 gap-y-1 border-b border-line py-1.5 text-small">
          <dt class="font-semibold">{{ t('kernel.settings.version') }}</dt>
          <!-- A build from source has a long version: cut short. -->
          <dd :class="[line, 'font-mono']" translate="no" :title="manifest.data.value.version">
            <data :value="manifest.data.value.version">{{ manifest.data.value.version }}</data>
          </dd>
          <dt class="font-semibold">{{ t('kernel.settings.build') }}</dt>
          <dd :class="[fact, 'flex flex-wrap justify-end gap-x-2 text-ink-muted']">
            <time v-if="manifest.data.value.date" :datetime="manifest.data.value.date">{{ intl.date(manifest.data.value.date) }}</time>
            <data :value="manifest.data.value.platform" class="font-mono">{{ manifest.data.value.platform }}</data>
          </dd>
          <dt class="font-semibold">{{ t('kernel.settings.source') }}</dt>
          <dd :class="[fact, 'flex flex-wrap justify-end gap-x-2']">
            <a :href="repository.url" target="_blank" rel="noopener" translate="no">{{ repository.name }}</a>
            <span class="text-ink-muted">{{ t('kernel.settings.license') }}</span>
          </dd>
        </dl>
        <div :class="[row, 'flex-wrap']">
          <div class="min-w-0 flex-[1_1_8rem]">
            <span class="block font-semibold">{{ t('kernel.settings.check_update') }}</span>
            <span :class="help" :title="t('kernel.settings.update_help')">{{ t('kernel.settings.update_help') }}</span>
          </div>
          <JecButton size="sm" class="ml-auto flex-none" :busy="release.isPending.value" @click="release.mutate()">
            <RefreshCw class="size-4" aria-hidden="true" />{{ t('kernel.settings.check_update') }}
          </JecButton>
        </div>
        <label :class="[row, 'cursor-pointer']">
          <span class="min-w-0 flex-1">
            <span class="block font-semibold">{{ t('kernel.settings.update_check') }}</span>
            <span :class="help" :title="t('kernel.settings.update_check_hint')">{{ t('kernel.settings.update_check_hint') }}</span>
          </span>
          <input
            type="checkbox"
            class="size-4 flex-none accent-brand"
            :checked="system.data.value?.updateCheck"
            :disabled="setUpdateCheck.isPending.value || !system.data.value"
            @change="setUpdateCheck.mutate(($event.target as HTMLInputElement).checked)"
          />
        </label>
        <JecAlert v-if="releaseError.alert.value" :code="releaseError.alert.value.code" :params="releaseError.alert.value.params" />
        <p v-else-if="release.data.value" role="status" class="m-0 py-1.5 text-small [overflow-wrap:anywhere]">
          <template v-if="!release.data.value.latest">{{ t('kernel.settings.no_release') }}</template>
          <span v-else-if="release.data.value.newer" class="font-semibold text-ok-ink">
            {{ t('kernel.settings.update_available', { version: release.data.value.latest }) }}
          </span>
          <template v-else-if="versioned">{{ t('kernel.settings.up_to_date') }}</template>
          <template v-else>{{ t('kernel.settings.latest_release', { version: release.data.value.latest }) }}</template>
          <template v-if="release.data.value.url && release.data.value.latest">
            {{ ' ' }}<a :href="release.data.value.url" target="_blank" rel="noopener">{{ t('kernel.settings.release_notes') }}</a>
          </template>
        </p>
        <div v-if="release.data.value?.newer" class="flex flex-col gap-1 pb-2 text-small">
          <span class="text-ink-muted">{{ t(system.data.value?.image ? 'kernel.settings.upgrade_image' : 'kernel.settings.upgrade_binary') }}</span>
          <span class="flex items-center gap-2">
            <code class="min-w-0 flex-1 rounded-sm bg-surface-sunken px-2 py-1 [overflow-wrap:anywhere]" translate="no">{{ upgradeCommand }}</code>
            <CopyButton :value="upgradeCommand" label="kernel.settings.upgrade_copy" />
          </span>
        </div>
      </section>
    </div>

    <div class="flex flex-col gap-3 sm:col-span-2 sm:grid sm:grid-cols-2 xl:col-span-1 xl:flex">
      <section v-if="session.data.value" aria-labelledby="account" :class="panel">
        <div class="flex flex-wrap items-baseline justify-between gap-x-3">
          <h2 id="account" :class="heading">{{ t('kernel.settings.account') }}</h2>
          <p class="m-0 min-w-0 text-small [overflow-wrap:anywhere]">
            <span class="text-ink-muted">{{ t('kernel.account.username') }}</span>
            {{ ' ' }}<span class="font-semibold" translate="no">{{ session.data.value.username }}</span>
          </p>
        </div>
        <p v-if="changed" role="status" class="m-0 inline-flex items-center gap-1 text-small text-ok-ink">
          <Check class="size-4 flex-none" aria-hidden="true" />{{ t('kernel.settings.password_done') }}
        </p>
        <p v-if="twoFactorDone" role="status" class="m-0 inline-flex items-center gap-1 text-small text-ok-ink">
          <Check class="size-4 flex-none" aria-hidden="true" />{{
            t(twoFactorDone === 'on' ? 'kernel.settings.two_factor_done' : 'kernel.settings.two_factor_off_done')
          }}
        </p>
        <JecAlert v-if="accountError.error.value" :code="accountError.error.value.code" :params="accountError.error.value.params" />
        <div :class="[row, 'flex-wrap']">
          <div class="min-w-0 flex-[1_1_8rem]">
            <span class="block font-semibold">{{ t('kernel.account.password') }}</span>
            <span v-if="session.data.value.passwordChangedAt" :class="help">
              {{ t('kernel.settings.password_changed_at', { date: intl.date(session.data.value.passwordChangedAt) }) }}
            </span>
          </div>
          <JecButton size="sm" class="ml-auto flex-none" @click="changing = true">
            <KeyRound class="size-4" aria-hidden="true" />{{ t('kernel.settings.change_password') }}
          </JecButton>
        </div>
        <div :class="[row, 'flex-wrap']">
          <div class="min-w-0 flex-[1_1_8rem]">
            <span class="block font-semibold">{{ t('kernel.settings.two_factor') }}</span>
            <span :class="help" :title="t('kernel.settings.two_factor_hint')">{{ t('kernel.settings.two_factor_hint') }}</span>
          </div>
          <span v-if="session.data.value.twoFactor" class="inline-flex flex-none items-center gap-1 text-small font-semibold text-ok-ink">
            <ShieldCheck class="size-4" aria-hidden="true" />{{ t('kernel.settings.two_factor_state_on') }}
          </span>
          <span v-else class="inline-flex flex-none items-center gap-1 text-small text-ink-muted">
            <ShieldOff class="size-4" aria-hidden="true" />{{ t('kernel.settings.two_factor_state_off') }}
          </span>
          <JecButton v-if="session.data.value.twoFactor" size="sm" class="ml-auto flex-none" @click="turningOff = true">
            {{ t('kernel.settings.two_factor_turn_off') }}
          </JecButton>
          <JecButton v-else size="sm" class="ml-auto flex-none" @click="turningOn = true">
            {{ t('kernel.settings.two_factor_turn_on') }}
          </JecButton>
        </div>
        <RecoveryCodes v-if="session.data.value.twoFactor" v-model:codes="newCodes" :row-class="row" :left="session.data.value.recoveryCodes ?? 0" />
        <OidcSettings v-if="oidcOn" :row-class="row" />
        <div :class="row">
          <div class="min-w-0 flex-1">
            <label for="session-days" class="block font-semibold">{{ t('kernel.settings.session_days') }}</label>
            <span id="session-days-help" :class="help">{{ t('kernel.settings.session_days_hint') }}</span>
          </div>
          <select
            id="session-days"
            :value="session.data.value.sessionDays"
            aria-describedby="session-days-help"
            :aria-invalid="accountError.error.value ? 'true' : undefined"
            :class="[control, 'flex-none']"
            @change="pickSessionDays"
          >
            <option v-for="d in sessionDays" :key="d" :value="d">{{ t('kernel.settings.days', { days: d }) }}</option>
          </select>
        </div>
        <div :class="[row, 'flex-wrap']">
          <div class="min-w-0 flex-[1_1_8rem]">
            <span class="block font-semibold">{{ t('kernel.settings.tokens') }}</span>
            <span :class="help" :title="t('kernel.settings.tokens_hint')">{{ t('kernel.settings.tokens_hint') }}</span>
          </div>
          <data v-if="tokens.data.value" :value="tokens.data.value.tokens.length" class="flex-none text-small text-ink-muted tabular-nums">
            {{ tokens.data.value.tokens.length }}
          </data>
          <JecButton size="sm" class="ml-auto flex-none" @click="tokensOpen = true">
            <KeySquare class="size-4" aria-hidden="true" />{{ t('kernel.settings.tokens_manage') }}
          </JecButton>
        </div>
        <SessionsSettings :row-class="row" />
      </section>

      <!-- How this JEC runs, a line each; the variable that sets each is in its title. -->
      <section v-if="system.data.value || health.data.value" aria-labelledby="runtime" :class="[panel, 'xl:flex-1']">
        <h2 id="runtime" :class="heading">{{ t('kernel.settings.runtime') }}</h2>
        <dl class="m-0 grid grid-cols-[auto_minmax(0,1fr)] items-baseline gap-x-4 gap-y-1 py-1.5 text-small">
          <template v-if="system.data.value">
            <dt class="font-semibold">{{ t('kernel.settings.data_dir') }}</dt>
            <dd :class="[line, 'font-mono text-caption']" translate="no" :title="system.data.value.dataDir">{{ system.data.value.dataDir }}</dd>
            <dt :class="variable" :title="t('kernel.settings.set_by', { name: variables.database })">{{ t('kernel.settings.database') }}</dt>
            <dd :class="fact" translate="no">{{ databases[system.data.value.database] }}</dd>
            <dt :class="variable" :title="t('kernel.settings.set_by', { name: variables.baseURL })">{{ t('kernel.settings.base_url') }}</dt>
            <dd v-if="system.data.value.baseURL" :class="[fact, 'font-mono text-caption']" translate="no">{{ system.data.value.baseURL }}</dd>
            <dd v-else :class="[fact, 'text-ink-muted']">{{ t('kernel.settings.not_set') }}</dd>
            <dt :class="variable" :title="t('kernel.settings.set_by', { name: variables.listen })">{{ t('kernel.settings.listen') }}</dt>
            <dd :class="[fact, 'flex flex-wrap justify-end gap-x-2']">
              <span class="font-mono text-caption" translate="no">{{ system.data.value.listen }}</span>
              <span v-if="system.data.value.httpListen" class="text-caption text-ink-muted">
                {{ t('kernel.settings.http_listen') }} <span class="font-mono" translate="no">{{ system.data.value.httpListen }}</span>
              </span>
            </dd>
            <dt :class="variable" :title="t('kernel.settings.set_by', { name: variables.trustedProxies })">{{ t('kernel.settings.trusted_proxies') }}</dt>
            <dd v-if="system.data.value.trustedProxies" :class="[line, 'font-mono text-caption']" translate="no" :title="system.data.value.trustedProxies">
              {{ system.data.value.trustedProxies }}
            </dd>
            <dd v-else :class="[fact, 'text-ink-muted']">{{ t('kernel.settings.not_set') }}</dd>
            <dt :class="variable" :title="t('kernel.settings.set_by', { name: variables.allowedClients })">{{ t('kernel.settings.allowed_clients') }}</dt>
            <dd v-if="system.data.value.allowedClients" :class="[line, 'font-mono text-caption']" translate="no" :title="system.data.value.allowedClients">
              {{ system.data.value.allowedClients }}
            </dd>
            <dd v-else :class="[fact, 'text-ink-muted']">{{ t('kernel.settings.allowed_any') }}</dd>
            <dt :class="variable" :title="t('kernel.settings.set_by', { name: variables.tls })">{{ t('kernel.settings.tls') }}</dt>
            <dd v-if="!system.data.value.tls" :class="[line, 'text-ink-muted']" :title="t('kernel.settings.tls_off')">{{ t('kernel.settings.tls_off') }}</dd>
            <dd v-else-if="system.data.value.tls.missing" :class="[fact, 'inline-flex items-center justify-end gap-1 font-semibold text-warn-ink']">
              <TriangleAlert class="size-4 flex-none" aria-hidden="true" />{{ t('kernel.settings.tls_missing') }}
            </dd>
            <dd v-else :class="[fact, 'flex flex-wrap justify-end gap-x-2']">
              <span class="font-mono text-caption" translate="no">{{ system.data.value.tls.names?.join(', ') }}</span>
              <span v-if="system.data.value.tls.notAfter" class="text-ink-muted">
                {{ t('kernel.settings.tls_until') }} <time :datetime="system.data.value.tls.notAfter">{{ intl.date(system.data.value.tls.notAfter) }}</time>
              </span>
            </dd>
            <dt :class="variable" :title="t('kernel.settings.set_by', { name: variables.hsts })">{{ t('kernel.settings.hsts') }}</dt>
            <dd :class="[fact, system.data.value.hsts ? 'font-semibold text-ok-ink' : 'text-ink-muted']">
              {{ t(system.data.value.hsts ? 'kernel.settings.hsts_on' : 'kernel.settings.hsts_off') }}
            </dd>
          </template>
          <template v-if="health.data.value">
            <dt class="font-semibold">{{ t('kernel.health.jobs') }}</dt>
            <dd :class="[fact, 'flex flex-wrap items-center justify-end gap-x-2']">
              <span v-if="failedJobs.length" class="inline-flex items-center gap-1 font-semibold text-warn-ink">
                <TriangleAlert class="size-4 flex-none" aria-hidden="true" />{{ t('kernel.health.failed', { count: failedJobs.length }) }}
              </span>
              <span v-else class="inline-flex items-center gap-1 text-ok-ink">
                <CircleCheck class="size-4 flex-none" aria-hidden="true" />{{ t('kernel.health.all_ok', { count: health.data.value.jobs.length }) }}
              </span>
              <button type="button" class="cursor-pointer border-0 bg-transparent p-0 text-ink underline" @click="jobsOpen = true">
                {{ t('kernel.health.details') }}
              </button>
            </dd>
            <template v-if="health.data.value.disk">
              <dt class="font-semibold">{{ t('kernel.health.disk') }}</dt>
              <dd :class="[line, diskLow ? 'font-semibold text-warn-ink' : '']" :title="diskText">
                <TriangleAlert v-if="diskLow" class="mr-1 inline size-4 align-[-3px]" aria-hidden="true" />{{ diskText }}
              </dd>
            </template>
          </template>
        </dl>
      </section>
    </div>
  </div>

  <JecDialog v-model:open="showingKey" :title="t('kernel.settings.secret_key')" small>
    <form id="secret-key-form" class="flex flex-col gap-3" novalidate @submit.prevent="submitKey">
      <template v-if="secretKey.data.value">
        <p class="m-0">{{ t(secretKey.data.value.from === 'env' ? 'kernel.settings.secret_key_from_env' : 'kernel.settings.secret_key_from_file') }}</p>
        <div class="flex items-center gap-2">
          <code class="min-w-0 flex-1 rounded-sm bg-surface-sunken px-2 py-1 text-small [overflow-wrap:anywhere]" translate="no">{{
            secretKey.data.value.key
          }}</code>
          <CopyButton :value="secretKey.data.value.key" label="kernel.settings.secret_key_copy" />
        </div>
        <p class="m-0 text-small text-ink-muted">{{ t('kernel.settings.secret_key_use') }}</p>
      </template>
      <template v-else>
        <p class="m-0">{{ t('kernel.settings.secret_key_intro') }}</p>
        <JecAlert v-if="secretKeyError.alert.value" :code="secretKeyError.alert.value.code" :params="secretKeyError.alert.value.params" />
        <JecField id="secret-key-password" label="kernel.account.confirm_password" :error="secretKeyError.fields.value.password">
          <JecInput
            id="secret-key-password"
            v-model="keyPassword"
            type="password"
            autocomplete="current-password"
            :invalid="!!secretKeyError.fields.value.password"
          />
        </JecField>
      </template>
    </form>
    <template #footer>
      <JecButton v-if="secretKey.data.value" variant="primary" @click="showingKey = false">{{ t('kernel.action.close') }}</JecButton>
      <template v-else>
        <JecButton @click="showingKey = false">{{ t('kernel.action.cancel') }}</JecButton>
        <JecButton type="submit" form="secret-key-form" variant="primary" :busy="secretKey.isPending.value" :disabled="!keyPassword">
          <KeyRound class="size-4" aria-hidden="true" />{{ t('kernel.settings.secret_key_show') }}
        </JecButton>
      </template>
    </template>
  </JecDialog>

  <JecDialog v-model:open="backingUp" :title="t('kernel.settings.backup_download')" small>
    <form id="backup-form" class="flex flex-col gap-3" novalidate @submit.prevent="submitBackup">
      <p class="m-0">{{ t('kernel.settings.backup_intro') }}</p>
      <JecAlert v-if="backupError.alert.value" :code="backupError.alert.value.code" :params="backupError.alert.value.params" />
      <JecField id="backup-password" label="kernel.account.confirm_password" :error="backupError.fields.value.password">
        <JecInput
          id="backup-password"
          v-model="backupPassword"
          type="password"
          autocomplete="current-password"
          required
          :invalid="!!backupError.fields.value.password"
        />
      </JecField>
    </form>
    <template #footer>
      <JecButton @click="backingUp = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton type="submit" form="backup-form" variant="primary" :busy="backup.isPending.value" :disabled="!backupPassword">
        <Download class="size-4" aria-hidden="true" />{{ t('kernel.settings.backup_download') }}
      </JecButton>
    </template>
  </JecDialog>

  <JecDialog v-model:open="exportingSecrets" :title="t('kernel.settings.config_export_secrets')" small>
    <form id="config-export-form" class="flex flex-col gap-3" novalidate @submit.prevent="submitExportSecrets">
      <p class="m-0">{{ t('kernel.config.secrets_intro') }}</p>
      <JecAlert v-if="exportError.alert.value" :code="exportError.alert.value.code" :params="exportError.alert.value.params" />
      <JecField id="config-export-password" label="kernel.account.confirm_password" :error="exportError.fields.value.password">
        <JecInput
          id="config-export-password"
          v-model="exportPassword"
          type="password"
          autocomplete="current-password"
          required
          :invalid="!!exportError.fields.value.password"
        />
      </JecField>
    </form>
    <template #footer>
      <JecButton @click="exportingSecrets = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton type="submit" form="config-export-form" variant="primary" :busy="exportConfig.isPending.value" :disabled="!exportPassword">
        <Download class="size-4" aria-hidden="true" />{{ t('kernel.settings.config_export') }}
      </JecButton>
    </template>
  </JecDialog>

  <JecDialog v-model:open="importing" :title="t('kernel.config.import_title')">
    <div class="flex flex-col gap-3">
      <p class="m-0">{{ t('kernel.config.import_intro') }}</p>
      <JecField id="config-file" label="kernel.config.file">
        <div class="flex min-w-0 items-center gap-2">
          <!-- A label of the panel's own in place of the browser's button, whose words are the browser's. -->
          <label
            for="config-file"
            class="inline-flex h-9 flex-none cursor-pointer items-center rounded-md border border-line-strong bg-surface-raised px-3 text-small font-semibold text-ink hover:bg-surface-sunken max-sm:h-11"
          >
            {{ t('kernel.config.choose') }}
          </label>
          <input id="config-file" type="file" accept=".yaml,.yml,application/yaml,text/yaml" class="sr-only" @change="pickConfig" />
          <span v-if="configFile" class="min-w-0 truncate text-small text-ink-muted" translate="no" :title="configFile.name">{{ configFile.name }}</span>
        </div>
      </JecField>
      <JecAlert v-if="importError.alert.value" :code="importError.alert.value.code" :params="importError.alert.value.params" />
      <template v-if="report">
        <p v-if="report.errors" class="m-0 inline-flex items-start gap-1.5 font-semibold text-danger-ink">
          <OctagonX class="mt-0.5 size-4 flex-none" aria-hidden="true" />{{ t('kernel.config.errors', { count: report.errors }) }}
        </p>
        <p v-else-if="!changes" class="m-0 text-ink-muted">{{ t('kernel.config.nothing') }}</p>
        <section v-for="s in report.sections" :key="s.key" :aria-labelledby="`config-${s.key}`" class="flex flex-col gap-1.5">
          <h3 :id="`config-${s.key}`" class="m-0 text-small font-semibold text-ink-muted">{{ t(s.title as MessageKey) }}</h3>
          <p v-if="kept(s).length" class="m-0 flex min-w-0 flex-wrap items-start gap-x-2 gap-y-1">
            <span
              :class="[
                'inline-flex h-[22px] flex-none items-center gap-1 rounded-sm pr-2 pl-1.5 text-caption font-semibold whitespace-nowrap',
                actionLooks.keep.cls,
              ]"
            >
              <component :is="actionLooks.keep.icon" class="size-3.5 flex-none" aria-hidden="true" />{{ t(actionLooks.keep.text) }}
            </span>
            <span v-if="kept(s).some((i) => i.name)" class="min-w-0 pt-px text-small text-ink-muted [overflow-wrap:anywhere]" translate="no">{{
              intl.list(kept(s).flatMap((i) => (i.name ? [i.name] : [])))
            }}</span>
          </p>
          <ul v-if="listed(s).length" class="m-0 flex list-none flex-col gap-1.5 p-0">
            <li v-for="(it, i) in listed(s)" :key="i" class="flex min-w-0 flex-col gap-1 rounded-md border border-line px-3 py-2">
              <div class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                <span
                  :class="[
                    'inline-flex h-[22px] flex-none items-center gap-1 rounded-sm pr-2 pl-1.5 text-caption font-semibold whitespace-nowrap',
                    actionLooks[it.action].cls,
                  ]"
                >
                  <component :is="actionLooks[it.action].icon" class="size-3.5 flex-none" aria-hidden="true" />{{ t(actionLooks[it.action].text) }}
                </span>
                <span v-if="it.name" class="min-w-0 font-semibold [overflow-wrap:anywhere]" translate="no">{{ it.name }}</span>
              </div>
              <p
                v-for="(p, j) in it.problems ?? []"
                :key="j"
                :class="['m-0 flex min-w-0 items-start gap-1.5 text-small', p.warning ? 'text-warn-ink' : 'text-danger-ink']"
              >
                <component :is="p.warning ? TriangleAlert : OctagonX" class="mt-0.5 size-4 flex-none" aria-hidden="true" />
                <span class="min-w-0 [overflow-wrap:anywhere]">
                  <span class="font-semibold">{{ t(p.warning ? 'kernel.config.problem.warning' : 'kernel.config.problem.error') }}</span>
                  <code v-if="p.field" class="mx-1 font-mono">{{ p.field }}</code>
                  {{ ' ' }}{{ problemText(p) }}
                </span>
              </p>
            </li>
          </ul>
        </section>
      </template>
    </div>
    <template #footer>
      <JecButton @click="importing = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton variant="primary" :busy="importConfig.isPending.value" :disabled="!report || report.errors > 0 || !changes" @click="applyConfig">
        <Upload class="size-4" aria-hidden="true" />{{ t('kernel.config.apply') }}
      </JecButton>
    </template>
  </JecDialog>

  <JecDialog v-model:open="tokensOpen" :title="t('kernel.tokens.title')">
    <div class="flex flex-col gap-3">
      <p class="m-0">{{ t('kernel.tokens.intro') }}</p>
      <div v-if="madeToken" role="status" class="flex flex-col gap-2 rounded-md bg-ok-soft p-3">
        <p class="m-0 text-small font-semibold text-ok-ink">{{ t('kernel.tokens.made_new', { name: madeToken.name }) }}</p>
        <div class="flex items-center gap-2">
          <code class="min-w-0 flex-1 rounded-sm bg-surface-raised px-2 py-1 text-small [overflow-wrap:anywhere]">{{ madeToken.token }}</code>
          <CopyButton :value="madeToken.token" label="kernel.tokens.copy" />
        </div>
      </div>
      <JecAlert v-if="deleteTokenError.error.value" :code="deleteTokenError.error.value.code" :params="deleteTokenError.error.value.params" />
      <p v-if="tokens.data.value && !tokens.data.value.tokens.length" class="m-0 text-ink-muted">{{ t('kernel.tokens.empty') }}</p>
      <ul v-else class="m-0 list-none p-0">
        <li v-for="tk in tokens.data.value?.tokens" :key="tk.id" class="flex flex-wrap items-center gap-2 border-b border-line py-2 last:border-b-0">
          <div class="min-w-0 flex-[1_1_12rem]">
            <span class="block font-semibold [overflow-wrap:anywhere]" translate="no">{{ tk.name }}</span>
            <span class="flex flex-wrap gap-x-2 text-caption text-ink-muted">
              <span class="font-semibold">{{ t(scopeText[tk.scope]) }}</span>
              <span>{{ t('kernel.tokens.made', { date: intl.date(tk.createdAt) }) }}</span>
              <span>{{ tk.lastUsedAt ? t('kernel.tokens.used', { date: intl.dateTime(tk.lastUsedAt) }) : t('kernel.tokens.never_used') }}</span>
              <span v-if="tk.expiresAt" :class="tokenEnded(tk.expiresAt) ? 'font-semibold text-danger-ink' : ''">
                {{ t(tokenEnded(tk.expiresAt) ? 'kernel.tokens.ended' : 'kernel.tokens.ends', { date: intl.date(tk.expiresAt) }) }}
              </span>
            </span>
          </div>
          <JecButton
            size="sm"
            class="ml-auto flex-none"
            :variant="confirmDelete === tk.id ? 'danger' : 'ghost'"
            :busy="deleteToken.isPending.value && confirmDelete === tk.id"
            :aria-label="t('kernel.tokens.delete', { name: tk.name })"
            :title="t('kernel.tokens.delete', { name: tk.name })"
            @click="removeToken(tk.id)"
          >
            <Trash2 class="size-4" aria-hidden="true" /><template v-if="confirmDelete === tk.id">{{ t('kernel.tokens.delete_confirm') }}</template>
          </JecButton>
        </li>
      </ul>
      <form id="token-form" class="flex flex-col gap-3 border-t border-line pt-3" novalidate @submit.prevent="submitToken">
        <h3 class="m-0 text-heading font-semibold">{{ t('kernel.tokens.new') }}</h3>
        <JecAlert v-if="tokenError.alert.value" :code="tokenError.alert.value.code" :params="tokenError.alert.value.params" />
        <!-- Two fields a line: the dialog stays short. -->
        <div class="grid gap-3 sm:grid-cols-2">
          <JecField id="token-name" label="kernel.tokens.name" hint="kernel.tokens.name_help" :error="tokenError.fields.value.name">
            <JecInput id="token-name" v-model="tokenForm.name" maxlength="64" required :invalid="!!tokenError.fields.value.name" />
          </JecField>
          <JecField id="token-scope" label="kernel.tokens.scope">
            <select id="token-scope" v-model="tokenForm.scope" :class="field">
              <option value="read">{{ t('kernel.tokens.scope_read') }}</option>
              <option value="write">{{ t('kernel.tokens.scope_write') }}</option>
            </select>
          </JecField>
          <JecField id="token-days" label="kernel.tokens.days">
            <select id="token-days" v-model.number="tokenForm.days" :class="field">
              <option v-for="d in tokenDays" :key="d" :value="d">{{ d ? t('kernel.settings.days', { days: d }) : t('kernel.tokens.forever') }}</option>
            </select>
          </JecField>
          <JecField id="token-password" label="kernel.account.confirm_password" :error="tokenError.fields.value.password">
            <JecInput
              id="token-password"
              v-model="tokenForm.password"
              type="password"
              autocomplete="current-password"
              required
              :invalid="!!tokenError.fields.value.password"
            />
          </JecField>
        </div>
      </form>
    </div>
    <template #footer>
      <JecButton @click="tokensOpen = false">{{ t('kernel.action.close') }}</JecButton>
      <JecButton
        type="submit"
        form="token-form"
        variant="primary"
        :busy="createToken.isPending.value"
        :disabled="!tokenForm.name.trim() || !tokenForm.password"
      >
        {{ t('kernel.tokens.create') }}
      </JecButton>
    </template>
  </JecDialog>

  <JecDialog v-model:open="restoring" :title="t('kernel.restore.title')" small>
    <div v-if="restoreState !== 'form'" role="status" class="flex flex-col gap-2">
      <p class="m-0 inline-flex items-center gap-2">
        <LoaderCircle v-if="restoreState === 'waiting'" class="size-4 flex-none animate-spin" aria-hidden="true" />{{ t('kernel.restore.waiting') }}
      </p>
      <p v-if="restoreState === 'slow'" class="m-0 text-warn-ink">{{ t('kernel.restore.slow') }}</p>
    </div>
    <form v-else id="restore-form" class="flex flex-col gap-3" novalidate @submit.prevent="submitRestore">
      <p class="m-0">{{ t('kernel.restore.intro') }}</p>
      <p class="m-0 text-small text-ink-muted">{{ t('kernel.restore.restart') }}</p>
      <JecAlert v-if="restoreError.alert.value" :code="restoreError.alert.value.code" :params="restoreError.alert.value.params" />
      <JecField id="restore-file" label="kernel.restore.file">
        <div class="flex min-w-0 items-center gap-2">
          <!-- A label of the panel's own in place of the browser's button, whose words are the browser's. -->
          <label
            for="restore-file"
            class="inline-flex h-9 flex-none cursor-pointer items-center rounded-md border border-line-strong bg-surface-raised px-3 text-small font-semibold text-ink hover:bg-surface-sunken max-sm:h-11"
          >
            {{ t('kernel.restore.choose') }}
          </label>
          <input id="restore-file" type="file" accept=".db" class="sr-only" @change="pickBackup" />
          <span v-if="restoreFile" class="min-w-0 truncate text-small text-ink-muted" translate="no" :title="restoreFile.name">{{ restoreFile.name }}</span>
        </div>
      </JecField>
      <JecField id="restore-password" label="kernel.account.confirm_password" :error="restoreError.fields.value.password">
        <JecInput
          id="restore-password"
          v-model="restorePassword"
          type="password"
          autocomplete="current-password"
          required
          :invalid="!!restoreError.fields.value.password"
        />
      </JecField>
    </form>
    <template v-if="restoreState === 'form'" #footer>
      <JecButton @click="restoring = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton
        type="submit"
        form="restore-form"
        variant="danger"
        :busy="restore.isPending.value"
        :disabled="!restoreFile || !restorePassword"
      >
        <RotateCcw class="size-4" aria-hidden="true" />{{ t('kernel.restore.submit') }}
      </JecButton>
    </template>
  </JecDialog>

  <JecDialog v-model:open="changing" :title="t('kernel.settings.change_password')" small>
    <form id="password-form" class="flex flex-col gap-3" novalidate @submit.prevent="submitPassword">
      <JecAlert v-if="changeError.alert.value" :code="changeError.alert.value.code" :params="changeError.alert.value.params" />
      <JecField id="current-password" label="kernel.settings.current_password" :error="changeError.fields.value.password">
        <JecInput
          id="current-password"
          v-model="pw.current"
          type="password"
          autocomplete="current-password"
          required
          :invalid="!!changeError.fields.value.password"
        />
      </JecField>
      <JecField
        id="new-password"
        label="kernel.settings.new_password"
        hint="kernel.account.password_hint"
        :hint-params="{ min: 8 }"
        :error="changeError.fields.value.newPassword"
      >
        <JecInput
          id="new-password"
          v-model="pw.next"
          type="password"
          autocomplete="new-password"
          minlength="8"
          required
          :invalid="!!changeError.fields.value.newPassword"
        />
      </JecField>
      <JecField id="password-again" label="kernel.account.password_again" :error="mismatch ? 'kernel.account.password_mismatch' : null">
        <JecInput id="password-again" v-model="pw.again" type="password" autocomplete="new-password" required :invalid="mismatch" />
      </JecField>
    </form>
    <template #footer>
      <JecButton @click="changing = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton
        type="submit"
        form="password-form"
        variant="primary"
        :busy="change.isPending.value"
        :disabled="!pw.current || !pw.next || !pw.again"
      >
        {{ t('kernel.settings.change_password') }}
      </JecButton>
    </template>
  </JecDialog>

  <JecDialog v-model:open="turningOn" :title="t('kernel.two_factor.title_on')" small>
    <form id="two-factor-on-form" class="flex flex-col gap-3" novalidate @submit.prevent="submitTurnOn">
      <JecAlert v-if="secretError.alert.value" :code="secretError.alert.value.code" :params="secretError.alert.value.params" />
      <JecAlert v-if="enableError.alert.value" :code="enableError.alert.value.code" :params="enableError.alert.value.params" />
      <JecField v-if="!secret.data.value" id="two-factor-password" label="kernel.account.confirm_password" :error="secretError.fields.value.password">
        <JecInput
          id="two-factor-password"
          v-model="tfPassword"
          type="password"
          autocomplete="current-password"
          required
          :invalid="!!secretError.fields.value.password"
        />
      </JecField>
      <template v-else>
        <p class="m-0">{{ t('kernel.two_factor.scan') }}</p>
        <img
          :src="secret.data.value.qr"
          :alt="t('kernel.two_factor.qr')"
          width="176"
          height="176"
          class="mx-auto block size-44 rounded-md border border-line bg-white"
        />
        <div class="min-w-0">
          <span class="block text-small font-semibold">{{ t('kernel.two_factor.key') }}</span>
          <code translate="no" class="block font-mono text-small select-all [overflow-wrap:anywhere]">{{ groupedKey }}</code>
        </div>
        <JecField id="two-factor-code" label="kernel.two_factor.code" :error="enableError.fields.value.code">
          <JecInput
            id="two-factor-code"
            v-model="tfCode"
            inputmode="numeric"
            autocomplete="one-time-code"
            maxlength="7"
            required
            :invalid="!!enableError.fields.value.code"
          />
        </JecField>
        <p class="m-0 text-small text-ink-muted">
          {{ t('kernel.two_factor.lost') }} <code class="whitespace-nowrap">{{ resetCommand }}</code>
        </p>
      </template>
    </form>
    <template #footer>
      <JecButton @click="turningOn = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton
        type="submit"
        form="two-factor-on-form"
        variant="primary"
        :busy="secret.isPending.value || enable.isPending.value"
        :disabled="secret.data.value ? !tfCode : !tfPassword"
      >
        {{ t(secret.data.value ? 'kernel.settings.two_factor_turn_on' : 'kernel.two_factor.next') }}
      </JecButton>
    </template>
  </JecDialog>

  <JecDialog v-model:open="turningOff" :title="t('kernel.two_factor.title_off')" small>
    <form id="two-factor-off-form" class="flex flex-col gap-3" novalidate @submit.prevent="submitTurnOff">
      <p class="m-0">{{ t('kernel.two_factor.off_hint') }}</p>
      <JecAlert v-if="disableError.alert.value" :code="disableError.alert.value.code" :params="disableError.alert.value.params" />
      <JecField id="two-factor-off-password" label="kernel.account.confirm_password" :error="disableError.fields.value.password">
        <JecInput
          id="two-factor-off-password"
          v-model="tfPassword"
          type="password"
          autocomplete="current-password"
          required
          :invalid="!!disableError.fields.value.password"
        />
      </JecField>
    </form>
    <template #footer>
      <JecButton @click="turningOff = false">{{ t('kernel.action.cancel') }}</JecButton>
      <JecButton type="submit" form="two-factor-off-form" variant="danger" :busy="disable.isPending.value" :disabled="!tfPassword">
        {{ t('kernel.settings.two_factor_turn_off') }}
      </JecButton>
    </template>
  </JecDialog>
  <JobsDialog v-model:open="jobsOpen" :jobs="health.data.value?.jobs ?? []" :module-name="moduleName" />
</template>
