// Logo padrão (coruja). A logo personalizada (upload) substitui este componente a partir da Fase 5.
export default function Logo({ size = 36 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 64 64" role="img" aria-label="Coruja Backup">
      <rect width="64" height="64" rx="14" fill="var(--primary)" />
      <path d="M14 14l9 7h18l9-7v22c0 11-8 18-18 18S14 47 14 36z" fill="var(--accent)" />
      <circle cx="24" cy="31" r="7" fill="#fff" />
      <circle cx="40" cy="31" r="7" fill="#fff" />
      <circle cx="24" cy="31" r="3.2" fill="var(--primary)" />
      <circle cx="40" cy="31" r="3.2" fill="var(--primary)" />
      <path d="M29 38h6l-3 6z" fill="var(--primary)" />
    </svg>
  )
}
