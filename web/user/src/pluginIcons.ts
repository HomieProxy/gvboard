import type { Component } from 'vue'
import {
  BookOpenIcon, CalendarCheckIcon, CoinsIcon, FerrisWheelIcon, GiftIcon, KeyRoundIcon, MegaphoneIcon, PuzzleIcon, ShieldIcon,
  SparklesIcon, StarIcon, TicketIcon, TrophyIcon, UsersIcon,
} from '@lucide/vue'

/** 插件卡片与页面可用的图标，其它值按 puzzle 显示 */
const icons: Record<string, Component> = {
  puzzle: PuzzleIcon,
  gift: GiftIcon,
  'calendar-check': CalendarCheckIcon,
  star: StarIcon,
  trophy: TrophyIcon,
  ticket: TicketIcon,
  sparkles: SparklesIcon,
  coins: CoinsIcon,
  megaphone: MegaphoneIcon,
  'ferris-wheel': FerrisWheelIcon,
  key: KeyRoundIcon,
  users: UsersIcon,
  book: BookOpenIcon,
  shield: ShieldIcon,
}

export const pluginIcon = (name: string): Component => icons[name] || PuzzleIcon
