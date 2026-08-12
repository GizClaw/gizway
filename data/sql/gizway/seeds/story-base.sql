-- Deterministic GizWay-only fixture for black-box acceptance tests.
-- Customer identity, balances and ledger rows deliberately do not exist here.

INSERT INTO administrators (id,email,display_name,password_hash,status,created_at,updated_at) VALUES
('41000000-0000-4000-8000-000000000001','admin@gizway.test','Story Regional Administrator','$2y$10$PO.gPoH/.5ICr0hdws7NYeZ5Iz7EWQbINiTr70nxWnla9MPiQOoHa','active','2026-08-10T00:00:00.000000000Z','2026-08-10T00:00:00.000000000Z');
INSERT INTO admin_api_keys (id,administrator_id,name,key_prefix,secret_hash,status,created_at) VALUES
('51000000-0000-4000-8000-000000000001','41000000-0000-4000-8000-000000000001','Story Regional Admin Key','gizadm_story_admin',decode('55c85a39d2ad9897f5f689558eac0d4ee496adbffc13171f0c21333bf37db943','hex'),'active','2026-08-10T00:00:00.000000000Z');

INSERT INTO providers (id,slug,name,status,created_at,updated_at) VALUES
('61000000-0000-4000-8000-000000000001','fake-openai','Fake OpenAI','active','2026-08-10T00:00:00.000000000Z','2026-08-10T00:00:00.000000000Z');
INSERT INTO provider_endpoints (id,provider_id,name,base_url,credential_ref,region,priority,weight,status,created_at,updated_at) VALUES
('71000000-0000-4000-8000-000000000001','61000000-0000-4000-8000-000000000001','Story Fake Provider','http://127.0.0.1:1','story/fake-openai',NULL,100,100,'active','2026-08-10T00:00:00.000000000Z','2026-08-10T00:00:00.000000000Z');
INSERT INTO models (id,slug,name,modality,status,metadata,created_at,updated_at) VALUES
('81000000-0000-4000-8000-000000000001','story-text','Story Text Model','["text"]','active','{}','2026-08-10T00:00:00.000000000Z','2026-08-10T00:00:00.000000000Z');
INSERT INTO model_variants (id,model_id,provider_endpoint_id,provider_model_name,variant_slug,capabilities,context_window,max_output_tokens,status,created_at,updated_at) VALUES
('91000000-0000-4000-8000-000000000001','81000000-0000-4000-8000-000000000001','71000000-0000-4000-8000-000000000001','fake-text-v1','fake-openai','{"chat":true,"responses":true,"messages":true,"generateContent":true,"embeddings":true,"audio_speech":true,"audio_transcriptions":true,"image_generation":true,"realtime":true,"realtime_webrtc_callback":true}',8192,2048,'active','2026-08-10T00:00:00.000000000Z','2026-08-10T00:00:00.000000000Z');
INSERT INTO model_variant_prices (id,model_variant_id,metric,unit_size,upstream_cost_microcredits,base_customer_price_microcredits,customer_price_microcredits,discount_bps,valid_from,created_at) VALUES
('a1000000-0000-4000-8000-000000000001','91000000-0000-4000-8000-000000000001','input_token',1000,1000,2000,1800,1000,'2026-08-10T00:00:00.000000000Z','2026-08-10T00:00:00.000000000Z'),
('a1000000-0000-4000-8000-000000000002','91000000-0000-4000-8000-000000000001','output_token',1000,2000,4000,3600,1000,'2026-08-10T00:00:00.000000000Z','2026-08-10T00:00:00.000000000Z'),
('a1000000-0000-4000-8000-000000000003','91000000-0000-4000-8000-000000000001','cached_input_token',1000,500,1000,900,1000,'2026-08-10T00:00:00.000000000Z','2026-08-10T00:00:00.000000000Z'),
('a1000000-0000-4000-8000-000000000004','91000000-0000-4000-8000-000000000001','audio_second',1,10,20,18,1000,'2026-08-10T00:00:00.000000000Z','2026-08-10T00:00:00.000000000Z'),
('a1000000-0000-4000-8000-000000000005','91000000-0000-4000-8000-000000000001','image',1,50,100,90,1000,'2026-08-10T00:00:00.000000000Z','2026-08-10T00:00:00.000000000Z'),
('a1000000-0000-4000-8000-000000000007','91000000-0000-4000-8000-000000000001','input_audio_token',1000,1000,3000,2700,1000,'2026-08-10T00:00:00.000000000Z','2026-08-10T00:00:00.000000000Z'),
('a1000000-0000-4000-8000-000000000008','91000000-0000-4000-8000-000000000001','output_audio_token',1000,2000,6000,5400,1000,'2026-08-10T00:00:00.000000000Z','2026-08-10T00:00:00.000000000Z'),
('a1000000-0000-4000-8000-000000000006','91000000-0000-4000-8000-000000000001','request',1,5,10,9,1000,'2026-08-10T00:00:00.000000000Z','2026-08-10T00:00:00.000000000Z');

INSERT INTO rate_publications (id,region,revision,content_hash,status,gizpay_publication_id,effective_at,created_at,updated_at) VALUES
('source_story_global_1','global',1,decode('01','hex'),'active','ratepub_story_global_1','2026-08-10T00:00:00.000000000Z','2026-08-10T00:00:00.000000000Z','2026-08-10T00:00:00.000000000Z');
INSERT INTO rate_publication_versions (publication_id,rate_version_id,model_variant_id,public_model,metric,unit_size,base_price_microcredits,customer_price_microcredits,discount_bps) VALUES
('source_story_global_1','a1000000-0000-4000-8000-000000000001','91000000-0000-4000-8000-000000000001','story-text','input_token',1000,2000,1800,1000),
('source_story_global_1','a1000000-0000-4000-8000-000000000002','91000000-0000-4000-8000-000000000001','story-text','output_token',1000,4000,3600,1000),
('source_story_global_1','a1000000-0000-4000-8000-000000000003','91000000-0000-4000-8000-000000000001','story-text','cached_input_token',1000,1000,900,1000),
('source_story_global_1','a1000000-0000-4000-8000-000000000004','91000000-0000-4000-8000-000000000001','story-text','audio_second',1,20,18,1000),
('source_story_global_1','a1000000-0000-4000-8000-000000000005','91000000-0000-4000-8000-000000000001','story-text','image',1,100,90,1000),
('source_story_global_1','a1000000-0000-4000-8000-000000000007','91000000-0000-4000-8000-000000000001','story-text','input_audio_token',1000,3000,2700,1000),
('source_story_global_1','a1000000-0000-4000-8000-000000000008','91000000-0000-4000-8000-000000000001','story-text','output_audio_token',1000,6000,5400,1000),
('source_story_global_1','a1000000-0000-4000-8000-000000000006','91000000-0000-4000-8000-000000000001','story-text','request',1,10,9,1000);
