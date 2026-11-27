/**
 * 内联图标
 *
 * 依赖清单里没有图标库，Arco 的 @arco-design/web-react/icon 是独立包
 * 也未列入，因此这里用内联 SVG。好处是零额外依赖、可继承 currentColor、
 * 按需 tree-shaking；代价是需要手工维护路径数据，故只收录实际用到的图标。
 *
 * 全部取自 Lucide（ISC 许可）的 24×24 线性网格，统一 stroke-width 1.8，
 * 保证视觉重量一致。
 */

import type { JSX } from 'react'
import type { SVGProps } from 'react'

type IconProps = SVGProps<SVGSVGElement> & { size?: number }

function Icon({ size = 16, children, ...rest }: IconProps): JSX.Element {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.8}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
      {...rest}
    >
      {children}
    </svg>
  )
}

// ---------- 导航 ----------

export const IconTasks = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <rect x="3" y="4" width="7" height="7" rx="1.5" />
    <rect x="14" y="4" width="7" height="7" rx="1.5" />
    <rect x="3" y="15" width="7" height="5" rx="1.5" />
    <rect x="14" y="15" width="7" height="5" rx="1.5" />
  </Icon>
)

export const IconFolder = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2Z" />
  </Icon>
)

export const IconActivity = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <path d="M3 12h4l3 8 4-16 3 8h4" />
  </Icon>
)

export const IconFlow = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <rect x="3" y="4" width="6" height="5" rx="1.5" />
    <rect x="15" y="15" width="6" height="5" rx="1.5" />
    <rect x="3" y="15" width="6" height="5" rx="1.5" />
    <path d="M9 6.5h4a2 2 0 0 1 2 2v9" />
    <path d="M9 17.5h6" />
  </Icon>
)

// ---------- 操作 ----------

export const IconPlus = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <path d="M12 5v14M5 12h14" />
  </Icon>
)

export const IconPlay = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <path d="M7 4.5 19 12 7 19.5Z" />
  </Icon>
)

export const IconPause = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <path d="M9 5v14M15 5v14" />
  </Icon>
)

export const IconStop = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <rect x="6" y="6" width="12" height="12" rx="1.5" />
  </Icon>
)

export const IconTrash = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <path d="M4 7h16M10 4h4M6 7l1 13h10l1-13M10 11v6M14 11v6" />
  </Icon>
)

export const IconDownload = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <path d="M12 4v11M7.5 10.5 12 15l4.5-4.5M4 19h16" />
  </Icon>
)

export const IconUpload = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <path d="M12 20V9M7.5 13.5 12 9l4.5 4.5M4 5h16" />
  </Icon>
)

export const IconRefresh = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <path d="M20 11a8 8 0 0 0-13.7-5.3L4 8" />
    <path d="M4 5v3h3" />
    <path d="M4 13a8 8 0 0 0 13.7 5.3L20 16" />
    <path d="M20 19v-3h-3" />
  </Icon>
)

export const IconCopy = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <rect x="9" y="9" width="11" height="11" rx="2" />
    <path d="M15 5H6a2 2 0 0 0-2 2v9" />
  </Icon>
)

export const IconClose = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <path d="M6 6l12 12M18 6 6 18" />
  </Icon>
)

export const IconMore = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <circle cx="5" cy="12" r="1.4" fill="currentColor" stroke="none" />
    <circle cx="12" cy="12" r="1.4" fill="currentColor" stroke="none" />
    <circle cx="19" cy="12" r="1.4" fill="currentColor" stroke="none" />
  </Icon>
)

export const IconSearch = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <circle cx="11" cy="11" r="6.5" />
    <path d="m16 16 4 4" />
  </Icon>
)

// ---------- 视图与状态 ----------

export const IconGrid = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <rect x="3.5" y="3.5" width="7" height="7" rx="1.5" />
    <rect x="13.5" y="3.5" width="7" height="7" rx="1.5" />
    <rect x="3.5" y="13.5" width="7" height="7" rx="1.5" />
    <rect x="13.5" y="13.5" width="7" height="7" rx="1.5" />
  </Icon>
)

export const IconList = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <path d="M8 6h13M8 12h13M8 18h13M4 6h.01M4 12h.01M4 18h.01" />
  </Icon>
)

export const IconInfo = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <circle cx="12" cy="12" r="8.5" />
    <path d="M12 11v5M12 8h.01" />
  </Icon>
)

export const IconClock = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <circle cx="12" cy="12" r="8.5" />
    <path d="M12 7.5V12l3 2" />
  </Icon>
)

export const IconKey = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <circle cx="8" cy="14" r="3.5" />
    <path d="m10.5 11.5 6-6M15 7l2 2 2.5-2.5-2-2z" />
  </Icon>
)

export const IconTerminal = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <rect x="3" y="4" width="18" height="16" rx="2" />
    <path d="m7 10 2.5 2L7 14M12.5 14H17" />
  </Icon>
)

export const IconFile = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <path d="M13 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V9Z" />
    <path d="M13 3v6h6" />
  </Icon>
)

export const IconDocument = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <path d="M13 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V9Z" />
    <path d="M13 3v6h6M9 13h6M9 17h4" />
  </Icon>
)

export const IconSun = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <circle cx="12" cy="12" r="4" />
    <path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" />
  </Icon>
)

export const IconMoon = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <path d="M20 14.5A8.5 8.5 0 0 1 9.5 4a8.5 8.5 0 1 0 10.5 10.5Z" />
  </Icon>
)

export const IconChevronLeft = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <path d="m14 6-6 6 6 6" />
  </Icon>
)

export const IconChevronRight = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <path d="m10 6 6 6-6 6" />
  </Icon>
)

export const IconChevronDown = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <path d="m6 10 6 6 6-6" />
  </Icon>
)

export const IconArrowUp = (props: IconProps): JSX.Element => (
  <Icon {...props}>
    <path d="M12 20V5M6.5 10.5 12 5l5.5 5.5" />
  </Icon>
)

export const IconLogo = (props: IconProps): JSX.Element => (
  <Icon {...props} strokeWidth={1.6}>
    <path d="M12 2.5 20.5 7v10L12 21.5 3.5 17V7Z" />
    <path d="M12 7.5 16 10v4l-4 2.5L8 14v-4Z" />
  </Icon>
)
