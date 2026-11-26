import type { ReactNode } from 'react'

// Ícones simples (desenhados para este projeto) dos serviços de armazenamento. Cada um cabe em 48×48.
type P = { size?: number }
const wrap = (size: number, children: ReactNode, label: string) => (
  <svg width={size} height={size} viewBox="0 0 48 48" role="img" aria-label={label} focusable="false">{children}</svg>
)

export const GoogleDriveIcon = ({ size = 40 }: P) => wrap(size, <>
  <path d="M17 5h14l15 26H32z" fill="#FFBA00" />
  <path d="M17 5 2 31l7 12 15-26z" fill="#1EA362" />
  <path d="M9 43h30l7-12H16z" fill="#2684FC" />
</>, 'Google Drive')

export const OneDriveIcon = ({ size = 40 }: P) => wrap(size, <>
  <path d="M19 14a11 11 0 0 1 19 4 9 9 0 0 1-1 18H12a8 8 0 0 1-1-16 11 11 0 0 1 8-6z" fill="#0A64C9" />
  <path d="M19 14a11 11 0 0 0-8 6 8 8 0 0 0 1 16h13z" fill="#28A8EA" />
  <path d="M37 18a9 9 0 0 1 0 18H25V21a11 11 0 0 1 12-3z" fill="#1490DF" />
</>, 'OneDrive (Microsoft)')

export const DropboxIcon = ({ size = 40 }: P) => wrap(size, <g fill="#0061FF">
  <path d="M13 6 24 13 13 20 2 13z" /><path d="M35 6 46 13 35 20 24 13z" />
  <path d="M13 22 24 29 13 36 2 29z" /><path d="M35 22 46 29 35 36 24 29z" />
  <path d="M13 39 24 32 35 39 24 46z" />
</g>, 'Dropbox')

export const S3Icon = ({ size = 40 }: P) => wrap(size, <>
  <path d="M6 12c0-3 8-5 18-5s18 2 18 5l-4 27c0 2-6 4-14 4s-14-2-14-4z" fill="#E25444" />
  <ellipse cx="24" cy="12" rx="18" ry="5" fill="#F7A399" />
  <path d="M10 17c4 2 9 3 14 3s10-1 14-3l-1 7c-4 2-8 3-13 3s-9-1-13-3z" fill="#B63F31" opacity=".55" />
</>, 'Amazon S3 e compatíveis')

export const B2Icon = ({ size = 40 }: P) => wrap(size, <>
  <rect x="3" y="3" width="42" height="42" rx="10" fill="#E21E29" />
  <path d="M24 9c1 5 7 8 7 15a7 7 0 0 1-14 0c0-3 2-5 3-7 1 2 2 3 3 3 0-4-1-7 1-11z" fill="#fff" />
</>, 'Backblaze B2')

export const BoxIcon = ({ size = 40 }: P) => wrap(size, <>
  <rect x="3" y="9" width="42" height="30" rx="7" fill="#0061D5" />
  <text x="24" y="30" textAnchor="middle" fontFamily="system-ui,Arial,sans-serif" fontWeight="800" fontSize="16" fill="#fff">box</text>
</>, 'Box')

export const MegaIcon = ({ size = 40 }: P) => wrap(size, <>
  <circle cx="24" cy="24" r="21" fill="#D9272E" />
  <path d="M12 33V16l12 12 12-12v17h-4V24l-8 8-8-8v9z" fill="#fff" />
</>, 'MEGA')

export const PCloudIcon = ({ size = 40 }: P) => wrap(size, <>
  <path d="M17 13a11 11 0 0 1 20 5 9 9 0 0 1 0 18H13a8 8 0 0 1-1-16 10 10 0 0 1 5-7z" fill="#17BED0" />
  <path d="M21 17v14m0-12h5a4 4 0 0 1 0 8h-5" fill="none" stroke="#fff" strokeWidth="3.4" strokeLinecap="round" />
</>, 'pCloud')

export const SftpIcon = ({ size = 40 }: P) => wrap(size, <>
  <rect x="6" y="6" width="36" height="15" rx="4" fill="#5B6B8C" /><rect x="6" y="27" width="36" height="15" rx="4" fill="#7A89A8" />
  <circle cx="13" cy="13.5" r="2.2" fill="#9DF0B0" /><circle cx="13" cy="34.5" r="2.2" fill="#9DF0B0" />
  <path d="M25 13.5h11M25 34.5h11" stroke="#DDE6F5" strokeWidth="2.4" strokeLinecap="round" />
</>, 'Servidor SFTP')

export const SmbIcon = ({ size = 40 }: P) => wrap(size, <>
  <path d="M4 13a4 4 0 0 1 4-4h10l4 5h18a4 4 0 0 1 4 4v18a4 4 0 0 1-4 4H8a4 4 0 0 1-4-4z" fill="#E0A526" />
  <path d="M4 20h40v15a4 4 0 0 1-4 4H8a4 4 0 0 1-4-4z" fill="#F5C451" />
  <circle cx="24" cy="30" r="4" fill="#fff" opacity=".9" /><path d="M14 36h20" stroke="#fff" strokeWidth="2.4" strokeLinecap="round" opacity=".9" />
</>, 'Pasta de rede (SMB/NFS)')

export const FtpIcon = ({ size = 40 }: P) => wrap(size, <>
  <rect x="3" y="8" width="42" height="32" rx="7" fill="#2E7D5B" />
  <path d="M15 31V19m0 0-4 4m4-4 4 4M33 17v12m0 0-4-4m4 4 4-4" fill="none" stroke="#fff" strokeWidth="3.4" strokeLinecap="round" strokeLinejoin="round" />
</>, 'FTP')

export const LocalIcon = ({ size = 40 }: P) => wrap(size, <>
  <path d="M8 10h32l5 18v10a3 3 0 0 1-3 3H6a3 3 0 0 1-3-3V28z" fill="#59606E" />
  <path d="M3 28h42v10a3 3 0 0 1-3 3H6a3 3 0 0 1-3-3z" fill="#3F4653" />
  <circle cx="37" cy="34.5" r="2.4" fill="#7CE0A0" /><path d="M10 34.5h15" stroke="#C9CFDA" strokeWidth="2.6" strokeLinecap="round" />
</>, 'Pasta local ou volume montado')

export const CLOUD_ICON: Record<string, (p: P) => ReactNode> = {
  gdrive: GoogleDriveIcon, onedrive: OneDriveIcon, dropbox: DropboxIcon, s3: S3Icon, b2: B2Icon, box: BoxIcon, mega: MegaIcon, pcloud: PCloudIcon, sftp: SftpIcon, ftp: FtpIcon, smb: SmbIcon, local: LocalIcon,
}
