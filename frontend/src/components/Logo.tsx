interface LogoProps {
  size?: number
  className?: string
}

export default function Logo({ size = 30, className }: LogoProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 48 48"
      fill="none"
      className={className}
      role="img"
      aria-label="c2tool logo"
    >
      <defs>
        <linearGradient id="sliver-grad" x1="6" y1="4" x2="42" y2="44" gradientUnits="userSpaceOnUse">
          <stop stopColor="#7c5cff" />
          <stop offset="1" stopColor="#b794f5" />
        </linearGradient>
      </defs>
      <path
        d="M24 4C15.7 4 9 10.7 9 19v18.2c0 1.6 1.9 2.5 3.1 1.5l3.3-2.6 3.4 2.6c1.3 1 3 1 4.3 0l3.4-2.6 3.4 2.6c1.3 1 3 1 4.3 0l3.3-2.6 3.3 2.6c1.2 1 3.1.1 3.1-1.5V19C39 10.7 32.3 4 24 4z"
        fill="url(#sliver-grad)"
      />
      <circle cx="17.5" cy="20" r="2.6" fill="#fff" />
      <circle cx="30.5" cy="20" r="2.6" fill="#fff" />
      <path d="M17 31h14" stroke="#fff" strokeWidth="2.6" strokeLinecap="round" />
      <path
        d="M22.5 28l-2.7 3 2.7 3"
        stroke="#fff"
        strokeWidth="2.6"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  )
}
