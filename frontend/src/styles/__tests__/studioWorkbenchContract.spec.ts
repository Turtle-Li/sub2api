import fs from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'

const stylesDir = path.resolve(__dirname, '..')
const featuresDir = path.resolve(__dirname, '../../features')

const workbenchCss = fs.readFileSync(path.join(stylesDir, 'studio-workbench.css'), 'utf8')
const imageStudioTsx = fs.readFileSync(path.join(featuresDir, 'image-studio/Studio.tsx'), 'utf8')
const imageInputBarTsx = fs.readFileSync(path.join(featuresDir, 'image-studio/upstream/components/InputBar.tsx'), 'utf8')
const imageModelPickerTsx = fs.readFileSync(path.join(featuresDir, 'image-studio/ModelPicker.tsx'), 'utf8')
const videoWorkspaceVue = fs.readFileSync(path.join(featuresDir, 'video-studio/VideoWorkspace.vue'), 'utf8')
const videoComposerVue = fs.readFileSync(path.join(featuresDir, 'video-studio/VideoComposer.vue'), 'utf8')
const videoGalleryVue = fs.readFileSync(path.join(featuresDir, 'video-studio/VideoGallery.vue'), 'utf8')

describe('shared studio workbench heading, label, and density contract (React + Vue)', () => {
  it('loads studio-workbench.css after local studio.css in both Image Studio and Video Studio', () => {
    const imgLocalIdx = imageStudioTsx.indexOf("import './studio.css'")
    const imgSharedIdx = imageStudioTsx.indexOf("import '@/styles/studio-workbench.css'")
    expect(imgLocalIdx).toBeGreaterThan(-1)
    expect(imgSharedIdx).toBeGreaterThan(imgLocalIdx)

    const vidLocalIdx = videoWorkspaceVue.indexOf("import './studio.css'")
    const vidSharedIdx = videoWorkspaceVue.indexOf("import '@/styles/studio-workbench.css'")
    expect(vidLocalIdx).toBeGreaterThan(-1)
    expect(vidSharedIdx).toBeGreaterThan(vidLocalIdx)
  })

  it('wires equivalent shared .wb-* heading, form-label, and density classes across both studios', () => {
    // Root scope
    expect(imageStudioTsx).toContain('className="image-studio wb-root"')
    expect(videoWorkspaceVue).toContain('class="video-studio vs-workspace wb-root"')

    // Composer header
    expect(imageStudioTsx).toContain('studio-composer-heading wb-rail-head')
    expect(videoComposerVue).toContain('vs-form-head wb-rail-head')

    // Composer body, field stack, fields, labels, and footer
    expect(imageInputBarTsx).toContain('studio-input-fields wb-composer-body wb-field-stack')
    expect(imageInputBarTsx).toContain('studio-field wb-field')
    expect(imageInputBarTsx).toContain('studio-prompt-heading wb-label-row')
    expect(imageInputBarTsx).toContain('studio-submit-area wb-composer-foot')
    expect(imageModelPickerTsx).toContain('studio-model-picker wb-field')
    expect(imageModelPickerTsx).toContain('studio-field-label wb-label')

    expect(videoComposerVue).toContain('vs-fields wb-composer-body')
    expect(videoComposerVue).toContain('vs-controls wb-field-stack')
    expect(videoComposerVue).toContain('vs-field wb-field')
    expect(videoComposerVue).toContain('vs-label-row wb-label-row')
    expect(videoComposerVue).toContain('vs-submit wb-composer-foot')

    // Gallery region & header
    expect(imageStudioTsx).toContain('studio-gallery wb-gallery')
    expect(imageStudioTsx).toContain('studio-toolbar wb-gallery-head')
    expect(imageStudioTsx).toContain('studio-gallery-heading wb-gallery-title')

    expect(videoGalleryVue).toContain('vs-gallery wb-gallery')
    expect(videoGalleryVue).toContain('vs-gallery-head wb-gallery-head')
    expect(videoGalleryVue).toContain('vs-title-line wb-gallery-title')
    expect(videoGalleryVue).toContain('vs-count wb-gallery-count')
  })

  it('scopes shared heading, label, and spacing tokens under both studios in studio-workbench.css', () => {
    expect(workbenchCss).toContain(':is(.image-studio, .video-studio).wb-root')
    expect(workbenchCss).toContain(':is(.image-studio, .video-studio) .wb-rail-head h2')
    expect(workbenchCss).toContain(':is(.image-studio, .video-studio) .wb-gallery-title h2')
    expect(workbenchCss).toContain(':is(.image-studio, .video-studio) .wb-label')
    expect(workbenchCss).toContain(':is(.image-studio, .video-studio) .wb-composer-body')
    expect(workbenchCss).toContain(':is(.image-studio, .video-studio) .wb-field-stack')
    expect(workbenchCss).toContain(':is(.image-studio, .video-studio) .wb-composer-foot')
  })
})
