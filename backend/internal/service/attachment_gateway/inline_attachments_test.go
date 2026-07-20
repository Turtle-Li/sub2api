package attachment_gateway

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInspectInlineAttachmentsCoversKnownAttachmentFields(t *testing.T) {
	body := []byte(`{
		"model":"gpt-test",
		"input":[
			{"type":"input_image","image_url":"data:image/png;base64,QUJDRA=="},
			{"type":"input_image","image_url":"https://example.test/image.png"},
			{"type":"input_file","filename":"report.pdf","file_data":"data:application/pdf;base64,QUJDRA=="},
			{"type":"input_audio","input_audio":{"data":"QUJDRA==","format":"wav"}},
			{"type":"input_video","data":"QUJDRA=="},
			{"type":"input_text","text":"data:image/png;base64,QUJDRA=="}
		]
	}`)

	stats, err := InspectInlineAttachments(body)

	require.NoError(t, err)
	require.Equal(t, 4, stats.Count)
	require.Equal(t, 1, stats.OptimizableImageCount)
	require.Equal(t, 4, stats.OptimizableImageBytes)
	require.Equal(t, 3, stats.UnsupportedCount)
}

func TestEstimatedBase64DecodedBytesIgnoresWhitespaceAndPadding(t *testing.T) {
	require.Equal(t, 4, estimatedBase64DecodedBytes("QUJDRA=="))
	require.Equal(t, 4, estimatedBase64DecodedBytes("QUJD\nRA=="))
	require.Equal(t, 3, estimatedBase64DecodedBytes("QUJD"))
	require.Zero(t, estimatedBase64DecodedBytes(""))
}
