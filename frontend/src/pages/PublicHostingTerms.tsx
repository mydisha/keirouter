// Hosting 9router terms at `/ketentuan/hosting-9router`, linked from the
// portal checkout and the public footer. Same layout as PublicTerms. Bump
// TERMS_VERSION together with hosting.TermsVersion in the backend.

import { useEffect, type ReactNode } from "react";
import { PublicLayout, TELEGRAM_URL } from "../components/PublicLayout";

const EFFECTIVE_DATE = "17 Oktober 2026";
const TERMS_VERSION = "2026-10-17";

const LINK = "text-[var(--link)] underline underline-offset-2 hover:opacity-80";

type Section = { id: string; title: string; body: ReactNode };

const SECTIONS: Section[] = [
  {
    id: "penerimaan",
    title: "Penerimaan ketentuan",
    body: (
      <>
        <p>
          Ketentuan ini mengatur layanan Hosting 9router dari Tokenizer dan melengkapi{" "}
          <a href="/ketentuan" className={LINK}>Ketentuan Layanan</a> umum Tokenizer. Jika ada yang berbeda, ketentuan
          ini yang berlaku untuk layanan hosting.
        </p>
        <p>
          Anda menyetujui ketentuan ini dengan mencentang kotak persetujuan saat memesan atau memperpanjang. Setiap pesanan
          dan perpanjangan mencatat versi ketentuan yang Anda setujui beserta waktunya.
        </p>
      </>
    ),
  },
  {
    id: "layanan",
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
    id: "aktivasi",
    title: "Aktivasi",
    body: (
      <>
        <p>
          Instance dibuat manual oleh admin setelah pembayaran berhasil, paling lambat 1x24 jam pada hari kerja. Masa
          aktif 30 hari mulai dihitung saat instance aktif, bukan saat pembayaran.
        </p>
        <p>Image dan versi 9router yang dipasang kami pilih, dan dapat kami perbarui sewaktu-waktu.</p>
      </>
    ),
  },
  {
    id: "tagihan",
    title: "Tagihan dan masa aktif",
    body: (
      <ul className="list-disc space-y-2 pl-5">
        <li>Harga berlaku per 30 hari dan ditampilkan di portal sebelum Anda membayar.</li>
        <li>Pembayaran dilakukan di muka melalui SumoPod.</li>
        <li>Perpanjangan menambah 30 hari dihitung dari tanggal berakhir yang sedang berjalan.</li>
        <li>Tidak ada perpanjangan otomatis. Anda perlu memperpanjang sendiri dari portal.</li>
      </ul>
    ),
  },
  {
    id: "kedaluwarsa",
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
    id: "pengembalian-dana",
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
    id: "tanggung-jawab-pengguna",
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
    id: "data",
    title: "Data dan cadangan",
    body: (
      <p>
        Kami tidak menjamin adanya cadangan (backup) data instance. Ekspor dan simpan konfigurasi 9router Anda sendiri
        secara berkala.
      </p>
    ),
  },
  {
    id: "ketersediaan",
    title: "Ketersediaan",
    body: (
      <p>
        Layanan diberikan sebaik mungkin (best effort), tanpa jaminan waktu aktif (SLA). Pemeliharaan server dapat
        membuat container dimulai ulang.
      </p>
    ),
  },
  {
    id: "penangguhan",
    title: "Penangguhan",
    body: (
      <p>
        Kami dapat menghentikan instance yang melanggar ketentuan ini atau membebani server secara berlebihan, tanpa
        pengembalian dana.
      </p>
    ),
  },
  {
    id: "batasan",
    title: "Batasan tanggung jawab",
    body: (
      <p>
        Sejauh diizinkan hukum, Tokenizer tidak bertanggung jawab atas kerugian akibat akun penyedia AI yang diblokir,
        kebocoran kredensial di sisi Anda, maupun bug pada 9router.
      </p>
    ),
  },
  {
    id: "perubahan",
    title: "Perubahan ketentuan",
    body: (
      <p>
        Kami dapat memperbarui ketentuan ini. Versi baru berlaku untuk pesanan atau perpanjangan berikutnya, dan Anda
        akan diminta menyetujuinya lagi saat itu. Pertanyaan dapat dikirim melalui{" "}
        <a href={TELEGRAM_URL} target="_blank" rel="noopener noreferrer" className={LINK}>komunitas Telegram</a>.
      </p>
    ),
  },
];

export default function PublicHostingTerms() {
  useEffect(() => {
    document.title = "Tokenizer - Ketentuan Layanan Hosting 9router";
  }, []);

  return (
    <PublicLayout>
      <header className="max-w-2xl">
        <p className="font-mono text-[11px] uppercase tracking-[1.2px] text-[var(--green)]">Legal</p>
        <h1 className="mt-2 text-[28px] font-semibold tracking-[-0.6px] text-[var(--ink)] sm:text-[34px]">
          Ketentuan Layanan Hosting 9router
        </h1>
        <p className="mt-2 text-[13px] text-[var(--muted)]">
          Berlaku sejak {EFFECTIVE_DATE} · <span className="font-mono">Versi {TERMS_VERSION}</span>
        </p>
      </header>

      <div className="mt-8 grid gap-8 lg:grid-cols-[220px_minmax(0,1fr)] lg:gap-12">
        <nav aria-label="Daftar isi" className="lg:sticky lg:top-20 lg:self-start">
          <p className="font-mono text-[11px] uppercase tracking-[1px] text-[var(--muted)]">Daftar isi</p>
          <ol className="mt-3 space-y-1.5 text-[13px]">
            {SECTIONS.map((s, i) => (
              <li key={s.id}>
                <a href={`#${s.id}`} className="flex gap-2 text-[var(--ink)] no-underline hover:underline">
                  <span className="w-5 shrink-0 font-mono text-[var(--muted)]">{i + 1}.</span>
                  {s.title}
                </a>
              </li>
            ))}
          </ol>
        </nav>

        <article className="max-w-2xl space-y-10">
          {SECTIONS.map((s, i) => (
            <section key={s.id} id={s.id} className="scroll-mt-20">
              <h2 className="text-[18px] font-semibold tracking-[-0.3px] text-[var(--ink)]">
                <span className="mr-2 font-mono text-[var(--muted)]">{i + 1}.</span>
                {s.title}
              </h2>
              <div className="mt-3 space-y-3 text-[15px] leading-relaxed text-[var(--muted)]">{s.body}</div>
            </section>
          ))}
        </article>
      </div>
    </PublicLayout>
  );
}
