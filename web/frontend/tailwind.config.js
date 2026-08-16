/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  darkMode: 'class',
  theme: {
    extend: {
      fontFamily: {
        sans: ['Roboto', '"open sans"', '"Helvetica Neue"', 'Helvetica', 'Arial', 'sans-serif'],
      },
      colors: {
        // Arr-family surface ramp. Hex values sampled from Radarr's
        // open-source dark theme (values only, no code copied):
        //   950 pageBackground #202020 · 900 sidebar/header/modal #2a2a2a
        //   850 toolbar #262626 · 800 card/input #333333 · 700 border/hover
        //   600 dim #555 · 500 helpText #909293 · 400 disabled #999
        //   300 text #ccc · 200 lightGray #ddd · 100 buttonText #eee
        slate: {
          100: '#eeeeee',
          200: '#dddddd',
          300: '#cccccc',
          400: '#999999',
          500: '#909293',
          600: '#555555',
          700: '#404040',
          800: '#333333',
          850: '#262626',
          900: '#2a2a2a',
          950: '#202020',
        },
        // RomArr identity: a violet accent (distinct from Radarr-yellow /
        // Sonarr-blue). Exposed as CSS vars in index.css so it is swappable.
        accent: {
          DEFAULT: 'rgb(var(--accent) / <alpha-value>)',
          hover: 'rgb(var(--accent-hover) / <alpha-value>)',
          fg: 'rgb(var(--accent-fg) / <alpha-value>)',
          50: '#f5f3ff',
          100: '#ede9fe',
          200: '#ddd6fe',
          300: '#c4b5fd',
          400: '#a78bfa',
          500: '#8b5cf6',
          600: '#7c3aed',
          700: '#6d28d9',
          800: '#5b21b6',
          900: '#4c1d95',
        },
      },
      keyframes: {
        'fade-in': { from: { opacity: '0' }, to: { opacity: '1' } },
        'slide-in': {
          from: { opacity: '0', transform: 'translateY(6px)' },
          to: { opacity: '1', transform: 'translateY(0)' },
        },
        shimmer: {
          '100%': { transform: 'translateX(100%)' },
        },
      },
      animation: {
        'fade-in': 'fade-in 0.15s ease-out',
        'slide-in': 'slide-in 0.18s ease-out',
      },
    },
  },
  plugins: [],
}
