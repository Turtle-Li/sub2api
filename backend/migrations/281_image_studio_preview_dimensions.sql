-- Optional dimensions of the first indexed image (or a stored legacy cover).
-- Unknown rows remain NULL; gallery reads never open media to fill them.
ALTER TABLE batch_image_items
    ADD COLUMN preview_width INTEGER,
    ADD COLUMN preview_height INTEGER;
