/** @type {import('tailwindcss').Config} */
module.exports = {
  // Tell Tailwind to scan our UI files for classes
  content: [
    "./index.html",
    "./app.js",
  ],
  theme: {
    extend: {
      // Re-add our custom dark theme colors
      colors: {
        'gray-900': '#1a202c',
        'gray-800': '#2d3748',
        'gray-700': '#4a5568',
        'gray-300': '#d1d5db',
      }
    },
  },
  plugins: [],
}