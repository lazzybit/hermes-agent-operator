package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	api "github.com/lazzybit/hermes-agent-operator/api/v1alpha1"
	"io"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"net/http"
	"os/exec"
	"regexp"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"time"
)

type telegramBot struct {
	ID            int64  `json:"id"`
	Username      string `json:"username"`
	IsBot         bool   `json:"is_bot"`
	CanManageBots bool   `json:"can_manage_bots"`
}

func telegramCall(ctx context.Context, token, method string, payload any, out any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.telegram.org/bot"+token+"/"+method, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("invalid Telegram request")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("Telegram transport failure")
	}
	defer resp.Body.Close()
	var envelope struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("Telegram HTTP %d", resp.StatusCode)
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope); err != nil || !envelope.OK {
		return fmt.Errorf("Telegram API rejected request")
	}
	return json.Unmarshal(envelope.Result, out)
}
func generatedTelegramUsername(claim *api.ChannelClaim, class *api.ChannelClass) (string, error) {
	template := class.Spec.Parameters.UsernameTemplate
	if template == "" || claim.Spec.Name == "" || claim.UID == "" {
		return "", fmt.Errorf("usernameTemplate, claim spec.name and UID are required")
	}
	if !regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`).MatchString(claim.Spec.Name) {
		return "", fmt.Errorf("claim spec.name must be kebab case")
	}
	hash := sha256.Sum256([]byte(claim.UID))
	name := strings.NewReplacer("{NAME}", strings.ReplaceAll(claim.Spec.Name, "-", "_"), "{RANDOMIZED}", hex.EncodeToString(hash[:4])).Replace(template)
	if strings.ContainsAny(name, "{}") {
		return "", fmt.Errorf("usernameTemplate supports only {RANDOMIZED} and {NAME}")
	}
	if !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{4,31}$`).MatchString(name) {
		return "", fmt.Errorf("invalid generated Telegram username")
	}
	return name, nil
}
func (r *ChannelClaimReconciler) provisionTelegram(ctx context.Context, claim *api.ChannelClaim, class *api.ChannelClass, session *corev1.Secret) (string, error) {
	for _, key := range []string{"api_id", "api_hash", "session"} {
		if len(session.Data[key]) == 0 {
			return "", fmt.Errorf("MTProto session Secret requires api_id, api_hash and session")
		}
	}
	managerSecret := &corev1.Secret{}
	ref := class.Spec.Parameters.ManagerTokenSecretRef
	if err := r.Get(ctx, types.NamespacedName{Namespace: claim.Namespace, Name: ref.Name}, managerSecret); err != nil {
		return "", fmt.Errorf("manager credential unavailable")
	}
	token := strings.TrimSpace(string(managerSecret.Data[ref.Key]))
	if token == "" {
		return "", fmt.Errorf("manager token is empty")
	}
	var manager telegramBot
	if err := telegramCall(ctx, token, "getMe", map[string]any{}, &manager); err != nil {
		return "", err
	}
	if !manager.IsBot || !manager.CanManageBots {
		return "", fmt.Errorf("manager capability not enabled")
	}
	if claim.Status.GeneratedUsername == "" {
		name, err := generatedTelegramUsername(claim, class)
		if err != nil {
			return "", err
		}
		before := claim.DeepCopy()
		claim.Status.GeneratedUsername = name
		if err = r.Status().Patch(ctx, claim, client.MergeFrom(before)); err != nil {
			return "", err
		}
	}
	display := claim.Spec.DisplayName
	if display == "" {
		display = claim.Name
	}
	input := map[string]string{"api_id": string(session.Data["api_id"]), "api_hash": string(session.Data["api_hash"]), "session": string(session.Data["session"]), "manager_username": manager.Username, "username": claim.Status.GeneratedUsername, "display_name": display}
	encoded, _ := json.Marshal(input)
	operationCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(operationCtx, "/opt/telegram/bin/python", "/telegram_provision.py")
	cmd.Stdin = bytes.NewReader(encoded)
	// Never propagate stderr or raw stdout: the helper handles credentials.
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("MTProto provisioning failed; external identity must be verified before retry")
	}
	var bot struct {
		BotID       int64  `json:"bot_id"`
		Username    string `json:"username"`
		OwnerUserID int64  `json:"owner_user_id"`
		ManagerID   int64  `json:"manager_id"`
	}
	if json.Unmarshal(output, &bot) != nil || bot.BotID == 0 || bot.ManagerID != manager.ID || bot.Username != claim.Status.GeneratedUsername {
		return "", fmt.Errorf("MTProto returned an inconsistent identity")
	}
	var childToken string
	if err = telegramCall(ctx, token, "getManagedBotToken", map[string]any{"user_id": bot.BotID}, &childToken); err != nil {
		return "", err
	}
	var child telegramBot
	if err = telegramCall(ctx, childToken, "getMe", map[string]any{}, &child); err != nil {
		return "", err
	}
	if child.ID != bot.BotID || child.Username != bot.Username {
		return "", fmt.Errorf("child token identity mismatch")
	}
	name := "claim-" + string(claim.UID)
	if len(name) > 63 {
		return "", fmt.Errorf("claim UID exceeds resource naming budget")
	}
	secret := &corev1.Secret{}
	key := types.NamespacedName{Name: name, Namespace: claim.Namespace}
	if err = r.Get(ctx, key, secret); apierrors.IsNotFound(err) {
		secret = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: claim.Namespace, Annotations: map[string]string{"hermes.nousresearch.com/claim-uid": string(claim.UID)}}, Data: map[string][]byte{"token": []byte(childToken)}}
		err = r.Create(ctx, secret)
	} else if err == nil {
		if secret.Annotations["hermes.nousresearch.com/claim-uid"] != string(claim.UID) {
			return "", fmt.Errorf("refusing unrelated credential Secret")
		}
		secret.Data = map[string][]byte{"token": []byte(childToken)}
		err = r.Update(ctx, secret)
	}
	if err != nil {
		return "", err
	}
	channel := &api.Channel{}
	err = r.Get(ctx, key, channel)
	if apierrors.IsNotFound(err) {
		channel = &api.Channel{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: claim.Namespace}, Spec: api.ChannelSpec{ChannelClassName: class.Name, DisplayName: display, BotID: bot.BotID, OwnerUserID: bot.OwnerUserID, ClaimRef: &api.MessagingClaimRef{Kind: "ChannelClaim", Name: claim.Name, UID: claim.UID}, Telegram: api.TelegramChannelSpec{ID: bot.Username, Name: display, TokenSecretRef: corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: name}, Key: "token"}}}}
		err = r.Create(ctx, channel)
	} else if err == nil && (channel.Spec.ClaimRef == nil || channel.Spec.ClaimRef.UID != claim.UID || channel.Spec.BotID != bot.BotID) {
		return "", fmt.Errorf("refusing unrelated Channel")
	}
	return name, err
}
