// Hosting 9router terms: the "Hosting 9router" part of the terms page at
// /ketentuan#hosting, which the portal checkout links to. Bump
// HOSTING_TERMS_VERSION together with hosting.TermsVersion in the backend
// whenever this text changes materially.

import type { ReactNode } from "react";
import { TELEGRAM_URL } from "../components/PublicLayout";

export const HOSTING_EFFECTIVE_DATE = "17 Oktober 2026";
export const HOSTING_TERMS_VERSION = "2026-10-17";

const LINK = "text-[var(--link)] underline underline-offset-2 hover:opacity-80";

export type TermsSection = { id: string; title: string; body: ReactNode };

export const HOSTING_SECTIONS: TermsSection[] = [
  {
    id: "hosting-penerimaan",
    title: "Penerimaan ketentuan",
    body: (
      <>
        <p>
          Bagian ini berlaku khusus untuk layanan Hosting 9router dan melengkapi ketentuan di atas. Jika ada yang
          berbeda, bagian ini yang berlaku untuk layanan hosting.
        </p>
        <p>
          Anda menyetujui bagian ini dengan mencentang kotak persetujuan saat memesan atau memperpanjang. Setiap pesanan
          dan perpanjangan mencatat versi ketentuan yang Anda setujui beserta waktunya.
        </p>
      </>
    ),
  },
  {
    id: "hosting-layanan",
    title: "Layanan",
    body: (
      <>
        <p>
          Setiap pesanan mendapat satu container 9router yang berjalan di server kami, dengan subdomain HTTPS acak
          milik Anda sendiri.
        </p>
        <p>
          9router adalah perangkat lunak open-source buatan pihak ketiga. Kami hanya menyediakan tempat untuk
          menjalankannya dan tidak berafiliasi dengan pengembangnya. Kami tidak memberikan dukungan untuk konfigurasi,
          fitur, atau perilaku 9router itu sendiri.
        </p>
      </>
    ),
  },
  {
    id: "hosting-aktivasi",
    title: "Aktivasi",
    body: (
      <>
        <p>
          Instance dibuat manual oleh admin setelah pembayaran berhasil, paling lambat 1x24 jam pada hari kerja. Masa
          aktif 30 hari mulai dihitung saat instance aktif, bukan saat pembayaran.
        </p>
        <p>
          Instance memakai image 9router-go dari{" "}
          <a href="https://github.com/luqman-v1/9router-go" target="_blank" rel="noopener noreferrer" className={LINK}>
            github.com/luqman-v1/9router-go
          </a>
          . Versi yang dipasang kami pilih, dan dapat kami perbarui sewaktu-waktu.
        </p>
      </>
    ),
  },
  {
    id: "hosting-tagihan",
    title: "Tagihan dan masa aktif",
    body: (
      <ul className="list-disc space-y-2 pl-5">
        <li>Harga berlaku per 30 hari dan ditampilkan di portal sebelum Anda membayar.</li>
        <li>Pembayaran dilakukan di muka melalui portal.</li>
        <li>Perpanjangan menambah 30 hari dihitung dari tanggal berakhir yang sedang berjalan.</li>
        <li>Tidak ada perpanjangan otomatis. Anda perlu memperpanjang sendiri dari portal.</li>
      </ul>
    ),
  },
  {
    id: "hosting-kedaluwarsa",
    title: "Kedaluwarsa",
    body: (
      <p>
        Setelah tanggal berakhir, ada masa tenggang 3 hari. Setelah itu instance dihentikan otomatis. Perpanjangan selama
        masa tenggang dihitung dari tanggal berakhir. Perpanjangan setelah instance dihentikan dihitung dari tanggal
        pembayaran, dan instance dinyalakan kembali otomatis selama datanya belum dihapus. Data instance dapat dihapus
        permanen 7 hari setelah tanggal berakhir, dan tidak dapat dipulihkan.
      </p>
    ),
  },
  {
    id: "hosting-pengembalian-dana",
    title: "Pengembalian dana",
    body: (
      <p>
        Dana dikembalikan penuh hanya jika kami gagal mengaktifkan instance dalam 3x24 jam setelah pembayaran berhasil.
        Dana juga dikembalikan jika pembayaran kami terima untuk instance yang sudah dihapus. Setelah instance aktif,
        pembayaran tidak dapat dikembalikan, termasuk jika Anda berhenti memakainya sebelum masa aktif habis.
      </p>
    ),
  },
  {
    id: "hosting-tanggung-jawab-pengguna",
    title: "Tanggung jawab pengguna",
    body: (
      <>
        <p>
          Setiap instance dibuat dengan password awal acak yang hanya dapat Anda lihat di portal. Segera ganti password
          tersebut setelah login pertama, dan jaga kerahasiaannya. Siapa pun yang memegang password dashboard 9router
          Anda dapat mengubah seluruh isi instance.
        </p>
        <p>
          Anda bertanggung jawab penuh atas akun, kredensial, dan login OAuth penyedia AI yang Anda simpan di 9router,
          serta wajib mematuhi ketentuan masing-masing penyedia tersebut.
        </p>
        <p>Anda tidak boleh memakai instance untuk:</p>
        <ul className="list-disc space-y-2 pl-5">
          <li>Menjual kembali akses ke penyedia AI secara melanggar hukum atau ketentuan penyedia.</li>
          <li>Mengirim spam atau menyalahgunakan layanan pihak lain.</li>
          <li>Kegiatan lain yang melanggar hukum Republik Indonesia atau hukum yang berlaku bagi Anda.</li>
        </ul>
      </>
    ),
  },
  {
    id: "hosting-data",
    title: "Data dan cadangan",
    body: (
      <p>
        Kami tidak menjamin adanya cadangan (backup) data instance. Ekspor dan simpan konfigurasi 9router Anda sendiri
        secara berkala.
      </p>
    ),
  },
  {
    id: "hosting-ketersediaan",
    title: "Ketersediaan",
    body: (
      <p>
        Layanan diberikan sebaik mungkin (best effort), tanpa jaminan waktu aktif (SLA). Pemeliharaan server dapat
        membuat container dimulai ulang.
      </p>
    ),
  },
  {
    id: "hosting-penangguhan",
    title: "Penangguhan",
    body: (
      <p>
        Kami dapat menghentikan instance yang melanggar ketentuan ini atau membebani server secara berlebihan, tanpa
        pengembalian dana.
      </p>
    ),
  },
  {
    id: "hosting-batasan",
    title: "Batasan tanggung jawab",
    body: (
      <p>
        Sejauh diizinkan hukum, Tokenizer tidak bertanggung jawab atas kerugian akibat akun penyedia AI yang diblokir,
        kebocoran kredensial di sisi Anda, maupun bug pada 9router.
      </p>
    ),
  },
  {
    id: "hosting-perubahan",
    title: "Perubahan ketentuan",
    body: (
      <p>
        Kami dapat memperbarui bagian ini. Versi baru berlaku untuk pesanan atau perpanjangan berikutnya, dan Anda
        akan diminta menyetujuinya lagi saat itu. Pertanyaan dapat dikirim melalui{" "}
        <a href={TELEGRAM_URL} target="_blank" rel="noopener noreferrer" className={LINK}>komunitas Telegram</a>.
      </p>
    ),
  },
];
