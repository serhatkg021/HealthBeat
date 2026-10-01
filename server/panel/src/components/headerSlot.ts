import { createContext } from 'react'

// Sabit üst çubuktaki başlık yuvası: sayfalar başlıklarını (PageHeader) buraya çizer. Layout dışında (ör. test, giriş
// sayfaları) yuva yoktur ve başlık sayfanın içinde kalır.
export const HeaderSlotContext = createContext<HTMLElement | null>(null)
