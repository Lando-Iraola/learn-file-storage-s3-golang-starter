package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadVideo(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	fmt.Println("uploading video", videoID, "by user", userID)

	// TODO: implement the upload here

	const maxMemory = 1 << 30
	err = r.ParseMultipartForm(maxMemory)

	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't parse form data", err)
		return
	}

	mFile, mFileHeader, err := r.FormFile("video")
	defer mFile.Close()
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't find thumbnail", err)
		return
	}

	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't read video", err)
		return
	}

	video, err := cfg.db.GetVideo(videoID)

	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't find video", err)
		return
	}

	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "User is not authorized to modify this resource", err)
		return
	}

	mediaType := mFileHeader.Header.Get("Content-Type")
	if mediaType == "" {
		respondWithError(w, http.StatusBadRequest, "Missing Content-Type for humbnail", nil)
		return
	}

	mt, _, err := mime.ParseMediaType(mediaType)

	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Failed to parse Content-Type", err)
		return
	}

	if mt != "video/mp4" {
		respondWithError(w, http.StatusBadRequest, "Media-Type not accepted", nil)
		return
	}

	rndKey := make([]byte, 32)
	rand.Read(rndKey)
	rndKeyTxt := base64.RawURLEncoding.EncodeToString(rndKey)

	fileName := fmt.Sprintf("%s%s", rndKeyTxt, ".mp4")
	filePointer, err := os.CreateTemp(cfg.assetsRoot, "tabuly-video")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to create file", err)
		return
	}

	_, err = io.Copy(filePointer, mFile)
	defer os.Remove(filePointer.Name())
	defer filePointer.Close()
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to copy file contents", err)
		return
	}

	prefix, err := getVideoAspectRatio(filePointer.Name())
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Failed to calculate aspect ratio", err)
		return
	}

	fileName = fmt.Sprintf("%s/%s", prefix, fileName)
	slog.Info("This is what became of my prefix:", "fileName", fileName)

	fastVideoPath, err := processVideoForFastStart(filePointer.Name())
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't calculate moov start", err)
		return
	}

	fastVideo, err := os.Open(fastVideoPath)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't open fast video", err)
		return
	}

	defer os.Remove(fastVideo.Name())
	defer fastVideo.Close()

	fastVideo.Seek(0, io.SeekStart)
	cfg.s3Client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      &cfg.s3Bucket,
		Key:         &fileName,
		Body:        fastVideo,
		ContentType: &mt,
	},
	)

	url := fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", cfg.s3Bucket, cfg.s3Region, fileName)
	video.VideoURL = &url
	err = cfg.db.UpdateVideo(video)

	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Couldn't update video", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}
