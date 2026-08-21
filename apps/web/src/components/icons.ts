import type { IconNode as LucideIconNode } from 'lucide';
import {
  Activity,
  ArrowDown,
  ArrowLeft,
  ArrowUp,
  ArrowUpDown,
  Bot,
  Brain,
  Calendar,
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  ChevronUp,
  CircleAlert,
  CircleCheck,
  CircleHelp,
  CirclePlay,
  CircleStop,
  Clock,
  Code,
  Copy,
  Database,
  Download,
  Eye,
  EyeOff,
  FileCode,
  FileSpreadsheet,
  FileText,
  Filter,
  Flame,
  Globe,
  Info,
  LayoutDashboard,
  LayoutGrid,
  Lock,
  Menu,
  MessageSquare,
  MessageSquareQuote,
  Moon,
  MoreHorizontal,
  MoreVertical,
  Palette,
  Pencil,
  Play,
  Plus,
  Radio,
  RefreshCw,
  Save,
  Search,
  Server,
  Settings,
  SlidersHorizontal,
  Sparkles,
  Square,
  Sun,
  SunMedium,
  Table,
  Tag,
  Terminal,
  Trash2,
  Upload,
  User,
  Users,
  Wrench,
  X,
} from 'lucide';

export type IconChildNode = [tag: string, attrs: Record<string, string | number>];
export type IconNode = LucideIconNode | IconChildNode[];

const iconMap: Record<string, IconNode> = {
  // Navigation & Shell
  dashboard: LayoutDashboard,
  layout_dashboard: LayoutDashboard,
  forum: MessageSquare,
  message_square: MessageSquare,
  chat: MessageSquareQuote,
  message_square_quote: MessageSquareQuote,
  mark_chat_read: MessageSquare,
  psychology: Brain,
  brain: Brain,
  receipt_long: FileText,
  file_text: FileText,
  description: FileText,
  settings: Settings,
  ac_unit: Sparkles,
  sparkles: Sparkles,
  auto_awesome: Sparkles,
  light_mode: Sun,
  sun: Sun,
  dark_mode: Moon,
  moon: Moon,
  sun_medium: SunMedium,
  brightness_auto: SunMedium,
  menu: Menu,
  close: X,
  x: X,

  // Common UI actions & features
  search: Search,
  filter: Filter,
  filter_alt: Filter,
  add: Plus,
  plus: Plus,
  edit: Pencil,
  pencil: Pencil,
  delete: Trash2,
  trash: Trash2,
  trash2: Trash2,
  delete_sweep: Trash2,
  refresh: RefreshCw,
  refresh_cw: RefreshCw,
  download: Download,
  file_download: Download,
  upload: Upload,
  file_upload: Upload,
  user: User,
  person: User,
  account_circle: User,
  users: Users,
  group: Users,
  groups: Users,
  info: Info,
  check: Check,
  save: Save,
  copy: Copy,
  content_copy: Copy,
  flame: Flame,
  activity: Activity,
  sensors: Activity,
  radio: Radio,
  terminal: Terminal,
  bot: Bot,
  smart_toy: Bot,
  robot: Bot,
  code: Code,
  file_code: FileCode,
  deployed_code: Code,
  construction: Wrench,
  wrench: Wrench,
  dns: Server,
  server: Server,
  database: Database,
  palette: Palette,
  table: Table,
  table_chart: Table,
  spreadsheet: FileSpreadsheet,
  grid: LayoutGrid,

  // Visibility & Security
  eye: Eye,
  visibility: Eye,
  eye_off: EyeOff,
  visibility_off: EyeOff,
  lock: Lock,
  globe: Globe,
  public: Globe,
  tag: Tag,

  // Chevrons & Arrows
  chevron_left: ChevronLeft,
  chevron_right: ChevronRight,
  chevron_down: ChevronDown,
  chevron_up: ChevronUp,
  arrow_up_down: ArrowUpDown,
  swap_vert: ArrowUpDown,
  arrow_up: ArrowUp,
  arrow_upward: ArrowUp,
  arrow_down: ArrowDown,
  arrow_downward: ArrowDown,
  arrow_left: ArrowLeft,
  arrow_back: ArrowLeft,

  // Sliders, Modals & Misc
  sliders: SlidersHorizontal,
  sliders_horizontal: SlidersHorizontal,
  circle_alert: CircleAlert,
  warning: CircleAlert,
  error: CircleAlert,
  circle_check: CircleCheck,
  success: CircleCheck,
  help: CircleHelp,
  help_outline: CircleHelp,
  circle_help: CircleHelp,
  calendar: Calendar,
  clock: Clock,
  schedule: Clock,
  time: Clock,
  more: MoreHorizontal,
  more_horizontal: MoreHorizontal,
  more_vert: MoreVertical,
  play: Play,
  play_circle: CirclePlay,
  stop: Square,
  stop_circle: CircleStop,
};

export function renderLucideIcon(iconNode: IconNode, className = '', size?: number): string {
  const children = (iconNode as IconChildNode[])
    .map(([tag, attrs]: IconChildNode) => {
      const attrStr = Object.entries(attrs)
        .map(([k, v]) => `${k}="${v}"`)
        .join(' ');
      return `<${tag} ${attrStr}></${tag}>`;
    })
    .join('');

  // Handle standard size normalization
  let normalizedClass = className.trim();
  if (normalizedClass === 'sm') {
    normalizedClass = 'w-3.5 h-3.5';
  } else if (normalizedClass === 'md') {
    normalizedClass = 'w-4 h-4';
  } else if (normalizedClass === 'lg') {
    normalizedClass = 'w-5 h-5';
  }

  // Ensure default width/height attributes exist so SVG never grows unbounded
  const pixelSize = size || (normalizedClass.includes('w-3') || normalizedClass.includes('size-3') ? 14 : 16);
  const sizeAttrs = `width="${pixelSize}" height="${pixelSize}"`;

  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="lucide inline-block shrink-0 ${normalizedClass}" ${sizeAttrs}>${children}</svg>`;
}

export function icon(name: string, className = '', size?: number): string {
  const normalized = name.toLowerCase().replace(/-/g, '_');
  const node = iconMap[normalized] || iconMap.sparkles;
  return renderLucideIcon(node, className, size);
}
