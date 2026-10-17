// Terms of service at `/ketentuan`, linked from the public footer. Static
// copy only; styling is scoped through `.landing-root` tokens like the other
// public pages.

import { useEffect, type ReactNode } from "react";
import { PublicLayout, TELEGRAM_URL } from "../components/PublicLayout";

const EFFECTIVE_DATE = "22 Oktober 2026";

const LINK = "text-[var(--link)] underline underline-offset-2 hover:opacity-80";

type Section = { id: string; title: string; body: ReactNode };

const SECTIONS: Section[] = [
  {
    id: "penerimaan",
    title: "Penerimaan ketentuan",
    body: (
      <>
        <p>
          Ketentuan ini mengatur penggunaan Tokenizer, termasuk situs, portal pengguna, dan API. Dengan membuat akun,
          memakai API key, atau mengisi saldo, Anda menyetujui ketentuan ini. Jika tidak setuju, jangan gunakan layanan.
        </p>
        <p>Anda menyatakan cakap secara hukum, atau mendapat izin dari pihak yang berwenang, untuk menerima ketentuan ini.</p>
        <p>
          Layanan Hosting 9router juga tunduk pada{" "}
          <a href="/ketentuan/hosting-9router" className={LINK}>Ketentuan Layanan Hosting 9router</a>, yang melengkapi
          ketentuan ini.
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
          Tokenizer adalah layanan untuk mengakses model AI dari berbagai penyedia pihak ketiga dengan satu API key. Key
          ini mendukung format API OpenAI dan Anthropic. Anda membayar sesuai jumlah token yang diproses, tanpa biaya
          bulanan.
        </p>
        <p>
          Batas pemakaian dapat berubah. Model bisa berstatus habis jika tidak ada penyedia yang dapat melayaninya.
        </p>
        <p>
          Token yang sudah diproses penyedia tetap ditagih walaupun koneksi Anda terputus sebelum respons diterima, baik
          karena dibatalkan, kena timeout di sisi klien, maupun gangguan jaringan. Untuk permintaan non-streaming, model
          tetap menyelesaikan jawabannya dan seluruh tokennya ditagih. Untuk permintaan streaming, yang ditagih adalah token
          yang sudah dihasilkan sampai koneksi terputus. Untuk prompt besar atau jawaban panjang, kami sarankan memakai
          mode streaming (<code className="font-mono text-[13px]">stream: true</code>) agar koneksi tidak terputus karena timeout.
        </p>
      </>
    ),
  },
  {
    id: "harga",
    title: "Harga dan pemilihan penyedia",
    body: (
      <>
        <p>
          Harga tiap model mengikuti pasar dan dapat naik atau turun kapan saja, tergantung ketersediaan dan biaya dari
          penyedia. Harga di halaman{" "}
          <a href="/model" className={LINK}>Model &amp; harga</a> adalah harga saat halaman itu Anda buka. Saldo dipotong
          dengan harga yang berlaku saat permintaan diproses, jadi biaya akhirnya bisa berbeda dari harga yang Anda lihat
          sebelumnya.
        </p>
        <p>
          Kami berupaya mengirim setiap permintaan ke penyedia termurah yang tersedia saat itu. Jika penyedia tersebut penuh
          atau gagal melayani, kami mengalihkan permintaan ke penyedia berikutnya, yang harganya bisa lebih tinggi.
        </p>
        <p>
          Untuk membatasi harga, Anda dapat mengatur diskon minimum per model di portal. Diskon dihitung dari harga resmi
          model. Jika tidak ada penyedia yang dapat memberi diskon sebesar itu, permintaan ditolak dan saldo tidak dipotong.
          Pengaturan ini hanya berlaku untuk model yang mendukungnya. Portal menandai model yang belum mendukung.
        </p>
      </>
    ),
  },
  {
    id: "akun",
    title: "Akun dan API key",
    body: (
      <>
        <p>
          API key bersifat rahasia. Semua permintaan yang memakai key Anda dianggap berasal dari Anda, dan biayanya
          dipotong dari saldo Anda. Jangan menaruh key di kode yang dapat dibaca publik.
        </p>
        <p>
          Jika key bocor, segera buat ulang (rotate) di portal. Kami tidak mengganti saldo yang terpakai sebelum key
          diganti.
        </p>
      </>
    ),
  },
  {
    id: "saldo",
    title: "Saldo dan pembayaran",
    body: (
      <>
        <ul className="list-disc space-y-2 pl-5">
          <li>Saldo dalam rupiah dan diisi melalui portal dengan QRIS, mulai Rp 10.000 hingga Rp 10.000.000 per transaksi.</li>
          <li>Tagihan QRIS berlaku 24 jam. Tagihan yang lewat batas waktu batal dengan sendirinya tanpa biaya.</li>
          <li>Setiap permintaan memotong saldo sesuai harga model yang berlaku saat permintaan diproses.</li>
          <li>Saldo tidak memiliki masa berlaku selama akun Anda aktif.</li>
          <li>Saldo tidak dapat diuangkan, dipindahkan ke akun lain, atau dikembalikan (non-refundable).</li>
        </ul>
        <p>
          Pengecualian: jika Anda membayar dua kali untuk tagihan yang sama, atau pembayaran berhasil tetapi saldo tidak
          bertambah, hubungi kami melalui{" "}
          <a href={TELEGRAM_URL} target="_blank" rel="noopener noreferrer" className={LINK}>Telegram</a> dengan bukti
          pembayaran. Kami akan menambahkan saldo atau mengembalikan dana setelah pembayaran terverifikasi.
        </p>
      </>
    ),
  },
  {
    id: "bansos",
    title: "Bansos (akses gratis)",
    body: (
      <p>
        Bansos adalah API key gratis yang dipakai bersama untuk mencoba layanan. Modelnya terbatas, kuotanya dibagi
        dengan pengguna lain, dan tidak ada jaminan ketersediaan. Kami dapat mengubah, membatasi, atau menonaktifkan Bansos
        kapan saja. Key Bansos dilarang dijual kembali atau dibagikan sebagai produk berbayar.
      </p>
    ),
  },
  {
    id: "larangan",
    title: "Penggunaan yang dilarang",
    body: (
      <>
        <p>Anda tidak boleh memakai Tokenizer untuk:</p>
        <ul className="list-disc space-y-2 pl-5">
          <li>Kegiatan yang melanggar hukum Republik Indonesia atau hukum yang berlaku bagi Anda.</li>
          <li>Membuat atau menyebarkan konten seksual yang melibatkan anak, dalam bentuk apa pun.</li>
          <li>Membuat malware, melakukan peretasan tanpa izin, penipuan, phishing, atau spam.</li>
          <li>Menyebarkan disinformasi, menyamar sebagai orang lain, atau melecehkan dan mengancam pihak tertentu.</li>
          <li>Melanggar hak kekayaan intelektual atau privasi orang lain.</li>
          <li>Menjual kembali akses atau saldo tanpa persetujuan tertulis dari kami.</li>
          <li>Membuat banyak akun untuk mengambil kuota gratis, atau mengakali batas pemakaian dan sistem penagihan.</li>
          <li>Mengganggu, membebani secara sengaja, atau mencoba membobol infrastruktur Tokenizer.</li>
        </ul>
        <p>Permintaan Anda juga tunduk pada kebijakan penggunaan penyedia model yang memprosesnya.</p>
      </>
    ),
  },
  {
    id: "konten",
    title: "Konten dan hasil AI",
    body: (
      <>
        <p>
          Isi yang Anda kirim dan hasil yang Anda terima tetap menjadi milik Anda, sepanjang diizinkan oleh penyedia model.
          Anda bertanggung jawab atas isi yang Anda kirim dan cara Anda memakai hasilnya.
        </p>
        <p>
          Hasil AI bisa keliru, tidak lengkap, atau usang. Periksa hasilnya sebelum dipakai, terutama untuk keputusan
          medis, hukum, keuangan, atau keselamatan.
        </p>
      </>
    ),
  },
  {
    id: "privasi",
    title: "Data dan privasi",
    body: (
      <>
        <p>
          Tokenizer tidak menyimpan isi prompt maupun respons model. Permintaan Anda diteruskan langsung ke penyedia model
          untuk diproses, lalu hasilnya dikembalikan kepada Anda.
        </p>
        <p>
          Yang kami catat hanya data teknis untuk penagihan dan pemantauan layanan: model yang dipakai, jumlah token, biaya,
          waktu, dan status permintaan. Penyedia model pihak ketiga tunduk pada kebijakan privasinya masing-masing.
        </p>
        <p>Kami memproses data pribadi sesuai Undang-Undang Nomor 27 Tahun 2022 tentang Pelindungan Data Pribadi.</p>
      </>
    ),
  },
  {
    id: "tanggung-jawab",
    title: "Ketersediaan dan batas tanggung jawab",
    body: (
      <>
        <p>
          Layanan diberikan sebagaimana adanya, tanpa jaminan waktu aktif (SLA). Gangguan dapat terjadi karena pemeliharaan
          atau karena penyedia model sedang bermasalah.
        </p>
        <p>
          Sejauh diizinkan hukum, Tokenizer tidak bertanggung jawab atas kerugian tidak langsung, kehilangan keuntungan, atau
          kehilangan data akibat penggunaan layanan. Total tanggung jawab kami paling banyak sebesar saldo yang Anda bayarkan
          dalam 30 hari sebelum kejadian.
        </p>
      </>
    ),
  },
  {
    id: "penangguhan",
    title: "Penangguhan dan penghentian",
    body: (
      <p>
        Kami dapat membatasi, menangguhkan, atau menutup akun dan API key yang melanggar ketentuan ini atau membahayakan
        layanan. Untuk pelanggaran berat, seperti penipuan pembayaran atau konten seksual yang melibatkan anak, saldo yang
        tersisa dapat hangus. Anda dapat berhenti memakai layanan kapan saja.
      </p>
    ),
  },
  {
    id: "perubahan",
    title: "Perubahan ketentuan",
    body: (
      <p>
        Kami dapat memperbarui ketentuan ini. Versi terbaru selalu ada di halaman ini dengan tanggal berlaku yang baru, dan
        perubahan penting kami umumkan di grup Telegram. Dengan tetap memakai layanan setelah perubahan berlaku, Anda
        menyetujui versi terbaru.
      </p>
    ),
  },
  {
    id: "kontak",
    title: "Hukum yang berlaku dan kontak",
    body: (
      <p>
        Ketentuan ini tunduk pada hukum Republik Indonesia. Kami mengutamakan penyelesaian sengketa secara musyawarah.
        Pertanyaan dan laporan dapat dikirim melalui{" "}
        <a href={TELEGRAM_URL} target="_blank" rel="noopener noreferrer" className={LINK}>komunitas Telegram</a>.
      </p>
    ),
  },
];

export default function PublicTerms() {
  useEffect(() => {
    document.title = "Tokenizer - Ketentuan Layanan";
  }, []);

  return (
    <PublicLayout>
      <header className="max-w-2xl">
        <p className="font-mono text-[11px] uppercase tracking-[1.2px] text-[var(--green)]">Legal</p>
        <h1 className="mt-2 text-[28px] font-semibold tracking-[-0.6px] text-[var(--ink)] sm:text-[34px]">Ketentuan Layanan</h1>
        <p className="mt-2 text-[13px] text-[var(--muted)]">Berlaku sejak {EFFECTIVE_DATE}</p>
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
